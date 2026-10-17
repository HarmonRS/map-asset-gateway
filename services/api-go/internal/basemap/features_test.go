package basemap

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestCheckHealthFlagsMissingPaths(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	tileRoot := filepath.Join(root, "tiles")
	vectorRoot := filepath.Join(root, "vectors")
	writeTestFile(t, filepath.Join(tileRoot, "google_image", "0", "0", "0.webp"), "tile")
	writeTestFile(t, filepath.Join(vectorRoot, "boundary.geojson"), `{"type":"FeatureCollection","features":[]}`)

	store := openTestStore(t, StoreConfig{
		SQLitePath:     filepath.Join(root, "basemap.db"),
		ScanRoot:       tileRoot,
		VectorScanRoot: vectorRoot,
		APIBaseURL:     "http://127.0.0.1:8910",
		TileBaseURL:    "http://127.0.0.1:8910",
		VectorBaseURL:  "http://127.0.0.1:8910",
	})
	if _, err := store.RunScan(ctx, defaultScanSourceCode); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if _, err := store.RunVectorScan(ctx); err != nil {
		t.Fatalf("vector scan: %v", err)
	}

	report, err := store.CheckHealth(ctx)
	if err != nil {
		t.Fatalf("check health: %v", err)
	}
	if report.VersionsMissing != 0 || report.VectorsMissing != 0 {
		t.Fatalf("expected everything healthy, got %+v", report)
	}

	// A directory that disappears must be reported before the first 404.
	if err := os.RemoveAll(filepath.Join(tileRoot, "google_image")); err != nil {
		t.Fatalf("remove tile root: %v", err)
	}
	if err := os.Remove(filepath.Join(vectorRoot, "boundary.geojson")); err != nil {
		t.Fatalf("remove vector: %v", err)
	}

	report, err = store.CheckHealth(ctx)
	if err != nil {
		t.Fatalf("check health after removal: %v", err)
	}
	if report.VersionsMissing != 1 || report.VectorsMissing != 1 {
		t.Fatalf("expected one missing of each, got %+v", report)
	}

	versionHealth, err := store.loadVersionHealth(ctx)
	if err != nil {
		t.Fatalf("load version health: %v", err)
	}
	assetHealth, err := store.loadVectorHealth(ctx)
	if err != nil {
		t.Fatalf("load vector health: %v", err)
	}
	foundMissing := false
	for _, health := range versionHealth {
		if health.Health == healthMissing {
			foundMissing = true
		}
	}
	if !foundMissing {
		t.Fatalf("expected a missing version verdict, got %+v", versionHealth)
	}
	foundMissing = false
	for _, health := range assetHealth {
		if health.Health == healthMissing {
			foundMissing = true
		}
	}
	if !foundMissing {
		t.Fatalf("expected a missing vector verdict, got %+v", assetHealth)
	}
}

func TestPushRetrySucceedsAfterBackoff(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	tileRoot := filepath.Join(root, "tiles")
	writeTestFile(t, filepath.Join(tileRoot, "google_image", "0", "0", "0.webp"), "tile")

	var attempts int
	callback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer callback.Close()

	store := openTestStore(t, StoreConfig{
		SQLitePath:    filepath.Join(root, "basemap.db"),
		ScanRoot:      tileRoot,
		APIBaseURL:    "http://127.0.0.1:8910",
		TileBaseURL:   "http://127.0.0.1:8910",
		VectorBaseURL: "http://127.0.0.1:8910",
	})
	if _, err := store.RunScan(ctx, defaultScanSourceCode); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if _, err := store.CreateTargetSystem(ctx, CreateTargetSystemInput{Code: "downstream", CallbackURL: callback.URL}); err != nil {
		t.Fatalf("create target: %v", err)
	}
	record, err := store.PushBasemapVersion(ctx, "downstream", "google_image", "current")
	if err != nil {
		t.Fatalf("push: %v", err)
	}

	// First attempt fails, so the record must be parked in retrying with a
	// scheduled next attempt rather than being dropped as failed.
	deadline := time.Now().Add(5 * time.Second)
	var parked bool
	for time.Now().Before(deadline) {
		records, err := store.ListPushRecords(ctx, 5)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for _, item := range records {
			if item.ID == record.ID && item.Status == pushStatusRetrying && item.NextAttemptAt != nil {
				parked = true
			}
		}
		if parked {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !parked {
		t.Fatal("expected the failed push to be rescheduled rather than dropped")
	}

	// Make the retry due immediately, then run the dispatcher.
	if _, err := store.db.ExecContext(ctx, `UPDATE push_records SET next_attempt_at = ? WHERE id = ?`, toRFC3339(nowUTC().Add(-time.Minute)), record.ID); err != nil {
		t.Fatalf("force due: %v", err)
	}
	if err := store.dispatchPushRetries(ctx); err != nil {
		t.Fatalf("dispatch retries: %v", err)
	}

	records, err := store.ListPushRecords(ctx, 5)
	if err != nil {
		t.Fatalf("list after retry: %v", err)
	}
	var final PushRecord
	for _, item := range records {
		if item.ID == record.ID {
			final = item
		}
	}
	if final.Status != pushStatusSucceeded {
		t.Fatalf("expected the retry to succeed, got status=%q message=%q", final.Status, final.ErrorMessage)
	}
	if final.AttemptCount != 2 {
		t.Fatalf("expected 2 attempts, got %d", final.AttemptCount)
	}
}

func TestManualRetryRequeuesFailedPush(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	tileRoot := filepath.Join(root, "tiles")
	writeTestFile(t, filepath.Join(tileRoot, "google_image", "0", "0", "0.webp"), "tile")

	callback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer callback.Close()

	store := openTestStore(t, StoreConfig{
		SQLitePath:    filepath.Join(root, "basemap.db"),
		ScanRoot:      tileRoot,
		APIBaseURL:    "http://127.0.0.1:8910",
		TileBaseURL:   "http://127.0.0.1:8910",
		VectorBaseURL: "http://127.0.0.1:8910",
	})
	if _, err := store.RunScan(ctx, defaultScanSourceCode); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if _, err := store.CreateTargetSystem(ctx, CreateTargetSystemInput{Code: "downstream", CallbackURL: callback.URL}); err != nil {
		t.Fatalf("create target: %v", err)
	}
	record, err := store.PushBasemapVersion(ctx, "downstream", "google_image", "current")
	if err != nil {
		t.Fatalf("push: %v", err)
	}

	// Exhaust the retry budget so the record lands in failed.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		records, _ := store.ListPushRecords(ctx, 5)
		for _, item := range records {
			if item.ID == record.ID && item.Status == pushStatusFailed {
				goto failed
			}
		}
		if _, err := store.db.ExecContext(ctx, `UPDATE push_records SET next_attempt_at = ? WHERE id = ? AND status = ?`, toRFC3339(nowUTC().Add(-time.Minute)), record.ID, pushStatusRetrying); err != nil {
			t.Fatalf("force due: %v", err)
		}
		_ = store.dispatchPushRetries(ctx)
		time.Sleep(20 * time.Millisecond)
	}

failed:
	records, err := store.ListPushRecords(ctx, 5)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var failedRecord PushRecord
	for _, item := range records {
		if item.ID == record.ID {
			failedRecord = item
		}
	}
	if failedRecord.Status != pushStatusFailed {
		t.Fatalf("expected a failed record, got %q", failedRecord.Status)
	}
	if failedRecord.AttemptCount != pushMaxAttempts {
		t.Fatalf("expected %d attempts, got %d", pushMaxAttempts, failedRecord.AttemptCount)
	}

	if err := store.RetryPush(ctx, "basemap", record.ID); err != nil {
		t.Fatalf("manual retry: %v", err)
	}
	records, err = store.ListPushRecords(ctx, 5)
	if err != nil {
		t.Fatalf("list after manual retry: %v", err)
	}
	for _, item := range records {
		if item.ID != record.ID {
			continue
		}
		if item.AttemptCount != 0 {
			t.Fatalf("expected the manual retry to reset the attempt counter, got %d", item.AttemptCount)
		}
		if item.Status == pushStatusSucceeded {
			t.Fatal("a failing target must not report success")
		}
	}
}

func TestCreateTileJobValidation(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	tileRoot := filepath.Join(root, "tiles")
	source := filepath.Join(root, "input.tif")
	writeTestFile(t, source, "fake raster")

	store := openTestStore(t, StoreConfig{
		SQLitePath:    filepath.Join(root, "basemap.db"),
		ScanRoot:      tileRoot,
		APIBaseURL:    "http://127.0.0.1:8910",
		TileBaseURL:   "http://127.0.0.1:8910",
		VectorBaseURL: "http://127.0.0.1:8910",
	})

	if _, err := store.CreateTileJob(ctx, CreateTileJobInput{TargetCode: "x", SourcePath: ""}); err == nil {
		t.Fatal("expected a missing source path to be rejected")
	}
	if _, err := store.CreateTileJob(ctx, CreateTileJobInput{SourcePath: source}); err == nil {
		t.Fatal("expected a missing target code to be rejected")
	}
	if _, err := store.CreateTileJob(ctx, CreateTileJobInput{SourcePath: source, TargetCode: "x", ZoomMin: 5, ZoomMax: 2}); err == nil {
		t.Fatal("expected an inverted zoom range to be rejected")
	}
	if _, err := store.CreateTileJob(ctx, CreateTileJobInput{SourcePath: filepath.Join(root, "missing.tif"), TargetCode: "x"}); err == nil {
		t.Fatal("expected a missing source file to be rejected")
	}

	job, err := store.CreateTileJob(ctx, CreateTileJobInput{SourcePath: source, TargetCode: "demo", ZoomMin: 0, ZoomMax: 3})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	if job.Status != tileJobPending {
		t.Fatalf("expected a pending job, got %q", job.Status)
	}
	if job.TileSize != 256 || job.TileFormat != "webp" {
		t.Fatalf("expected defaults to be applied, got %d/%s", job.TileSize, job.TileFormat)
	}
	if !strings.HasSuffix(job.OutputRoot, filepath.Join("tiles", "demo")) {
		t.Fatalf("unexpected output root %q", job.OutputRoot)
	}
}

func TestTileJobCommandArgsSubstitution(t *testing.T) {
	job := TileJob{
		SourcePath: "/data/in.tif",
		OutputRoot: "/data/out",
		ZoomMin:    1,
		ZoomMax:    5,
		TileSize:   512,
		TileFormat: "png",
		TargetCode: "demo",
	}
	args := tileJobCommandArgs(`gdal2tiles.py --xyz --zoom={zmin}-{zmax} --tilesize={tilesize} --format={format} {input} {output}`, job, job.SourcePath)
	want := []string{"gdal2tiles.py", "--xyz", "--zoom=1-5", "--tilesize=512", "--format=png", "/data/in.tif", "/data/out"}
	if len(args) != len(want) {
		t.Fatalf("expected %d args, got %v", len(want), args)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("arg %d = %q, want %q", i, args[i], want[i])
		}
	}
}

func TestSplitCommandLineKeepsQuotedSegments(t *testing.T) {
	parts := splitCommandLine(`cmd /c mkdir "C:\Program Files\tiles\0"`)
	if len(parts) != 4 {
		t.Fatalf("expected 4 parts, got %v", parts)
	}
	if parts[3] != `C:\Program Files\tiles\0` {
		t.Fatalf("quoted segment not preserved: %q", parts[3])
	}
}

func TestTileJobRunsCommandThenScans(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the stub slicer below uses cmd.exe")
	}
	ctx := context.Background()
	root := t.TempDir()
	tileRoot := filepath.Join(root, "tiles")
	source := filepath.Join(root, "input.tif")
	writeTestFile(t, source, "fake raster")
	if err := os.MkdirAll(tileRoot, 0o755); err != nil {
		t.Fatalf("mkdir tile root: %v", err)
	}

	store := openTestStore(t, StoreConfig{
		SQLitePath:    filepath.Join(root, "basemap.db"),
		ScanRoot:      tileRoot,
		APIBaseURL:    "http://127.0.0.1:8910",
		TileBaseURL:   "http://127.0.0.1:8910",
		VectorBaseURL: "http://127.0.0.1:8910",
		// Stand-in for gdal2tiles: lay down a z/x tree the scanner can read.
		TileJobCommand: `cmd /c mkdir "{output}\0\0"`,
	})
	if _, err := store.RunScan(ctx, defaultScanSourceCode); err != nil {
		t.Fatalf("initial scan: %v", err)
	}

	job, err := store.CreateTileJob(ctx, CreateTileJobInput{
		SourcePath: source,
		TargetCode: "demo",
		ZoomMin:    0,
		ZoomMax:    0,
	})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	store.runTileJob(ctx, job)

	tileJobs, err := store.ListTileJobs(ctx, 5)
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(tileJobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(tileJobs))
	}
	if tileJobs[0].Status != tileJobSucceeded {
		t.Fatalf("expected success, got %q (%s)", tileJobs[0].Status, tileJobs[0].Message)
	}

	basemaps, err := store.ListBasemaps(ctx)
	if err != nil {
		t.Fatalf("list basemaps: %v", err)
	}
	found := false
	for _, item := range basemaps {
		if item.Code == "demo" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the sliced basemap to be scanned in, got %+v", basemaps)
	}
}

func TestTileJobFailureIsRecorded(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	tileRoot := filepath.Join(root, "tiles")
	source := filepath.Join(root, "input.tif")
	writeTestFile(t, source, "fake raster")

	store := openTestStore(t, StoreConfig{
		SQLitePath:     filepath.Join(root, "basemap.db"),
		ScanRoot:       tileRoot,
		APIBaseURL:     "http://127.0.0.1:8910",
		TileBaseURL:    "http://127.0.0.1:8910",
		VectorBaseURL:  "http://127.0.0.1:8910",
		TileJobCommand: "definitely-not-a-real-binary {input} {output}",
	})
	job, err := store.CreateTileJob(ctx, CreateTileJobInput{SourcePath: source, TargetCode: "demo"})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	store.runTileJob(ctx, job)

	jobs, err := store.ListTileJobs(ctx, 5)
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if jobs[0].Status != tileJobFailed {
		t.Fatalf("expected failure, got %q", jobs[0].Status)
	}
	if jobs[0].Message == "" {
		t.Fatal("expected the failure reason to be recorded")
	}
	t.Logf("DEBUG job=%+v", jobs[0])
	if jobs[0].LogPath == "" {
		t.Fatal("expected a log path to be recorded")
	}
}

func TestRetryPushRejectsUnknownKind(t *testing.T) {
	store := openTestStore(t, StoreConfig{SQLitePath: filepath.Join(t.TempDir(), "basemap.db")})
	if err := store.RetryPush(context.Background(), "nonsense", "x"); err == nil {
		t.Fatal("expected an unknown kind to be rejected")
	}
}

func TestGetTargetSystemByIDMissing(t *testing.T) {
	store := openTestStore(t, StoreConfig{SQLitePath: filepath.Join(t.TempDir(), "basemap.db")})
	_, err := store.GetTargetSystemByID(context.Background(), "nope")
	if err == nil || errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected a not-found error, got %v", err)
	}
}

func TestBootstrapMigratesLegacySchema(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "legacy.db")

	// A database created before the health and retry columns existed.
	legacy, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath))
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	legacyDDL := []string{
		`CREATE TABLE basemap_versions (
			id TEXT PRIMARY KEY,
			basemap_id TEXT NOT NULL,
			scan_source_id TEXT,
			version TEXT NOT NULL,
			status TEXT NOT NULL,
			is_default INTEGER NOT NULL DEFAULT 0,
			manifest_path TEXT NOT NULL DEFAULT '',
			tile_root_path TEXT NOT NULL,
			url_template TEXT NOT NULL,
			tile_format TEXT NOT NULL,
			tile_scheme TEXT NOT NULL,
			min_zoom INTEGER NOT NULL DEFAULT 0,
			max_zoom INTEGER NOT NULL DEFAULT 0,
			bbox_json TEXT NOT NULL DEFAULT '',
			attribution TEXT NOT NULL DEFAULT '',
			metadata_json TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE push_records (
			id TEXT PRIMARY KEY,
			target_system_id TEXT NOT NULL,
			basemap_version_id TEXT NOT NULL,
			status TEXT NOT NULL,
			request_json TEXT NOT NULL DEFAULT '',
			response_status INTEGER,
			response_body TEXT NOT NULL DEFAULT '',
			error_message TEXT NOT NULL DEFAULT '',
			pushed_at TEXT NOT NULL,
			finished_at TEXT
		);`,
		`INSERT INTO basemap_versions (id, basemap_id, version, status, tile_root_path, url_template, tile_format, tile_scheme, created_at, updated_at)
		 VALUES ('v1', 'b1', 'current', 'ready', '/tiles/demo', '/tiles/demo/current/{z}/{x}/{y}.png', 'png', 'xyz', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');`,
	}
	for _, statement := range legacyDDL {
		if _, err := legacy.Exec(statement); err != nil {
			legacy.Close()
			t.Fatalf("seed legacy schema: %v", err)
		}
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	store := openTestStore(t, StoreConfig{SQLitePath: dbPath, HealthCheckInterval: time.Minute})

	// The pre-existing row must survive and gain default health values.
	var health, detail string
	if err := store.db.QueryRow(`SELECT health, health_detail FROM basemap_versions WHERE id = 'v1'`).Scan(&health, &detail); err != nil {
		t.Fatalf("legacy row lost or columns missing: %v", err)
	}
	if health != "" {
		t.Fatalf("expected a blank default health, got %q", health)
	}

	var attempts int
	var nextAttempt sql.NullString
	if err := store.db.QueryRow(`SELECT attempt_count, next_attempt_at FROM push_records LIMIT 1`).Scan(&attempts, &nextAttempt); err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("retry columns missing on push_records: %v", err)
	}

	if err := store.ensureColumn(context.Background(), "basemap_versions", "health", "TEXT NOT NULL DEFAULT ''"); err != nil {
		t.Fatalf("ensureColumn must be idempotent: %v", err)
	}
}

func TestTokenScopesNormalization(t *testing.T) {
	if got := normalizeTokenScopes(nil); got != "read" {
		t.Fatalf("expected read by default, got %q", got)
	}
	if got := normalizeTokenScopes([]string{"slice"}); got != "read,slice" {
		t.Fatalf("expected read,slice, got %q", got)
	}
	if got := normalizeTokenScopes([]string{"SLICE", "bogus", " read "}); got != "read,slice" {
		t.Fatalf("expected unknown scopes to be dropped, got %q", got)
	}

	store := openTestStore(t, StoreConfig{SQLitePath: filepath.Join(t.TempDir(), "basemap.db")})
	token, err := store.CreateToken(context.Background(), CreateTokenInput{
		Name:   "slicer",
		Scopes: []string{"slice"},
	})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	auth, err := store.AuthorizeToken(context.Background(), token.Token)
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if !auth.CanSlice() {
		t.Fatal("expected the token to carry the slice scope after a round trip")
	}
}

func TestCreateTileJobIsIdempotentByCode(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	tileRoot := filepath.Join(root, "tiles")
	source := filepath.Join(root, "input.tif")
	writeTestFile(t, source, "fake raster")

	store := openTestStore(t, StoreConfig{
		SQLitePath:    filepath.Join(root, "basemap.db"),
		ScanRoot:      tileRoot,
		APIBaseURL:    "http://127.0.0.1:8910",
		TileBaseURL:   "http://127.0.0.1:8910",
		VectorBaseURL: "http://127.0.0.1:8910",
	})

	input := CreateTileJobInput{Code: "repeat-me", SourcePath: source, TargetCode: "demo"}
	first, err := store.CreateTileJob(ctx, input)
	if err != nil {
		t.Fatalf("first submit: %v", err)
	}
	second, err := store.CreateTileJob(ctx, input)
	if err != nil {
		t.Fatalf("second submit must be accepted: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("expected the same job back, got %s then %s", first.ID, second.ID)
	}

	jobs, err := store.ListTileJobs(ctx, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("expected exactly one job, got %d", len(jobs))
	}
}

func TestGetTileJob(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	source := filepath.Join(root, "input.tif")
	writeTestFile(t, source, "fake raster")

	store := openTestStore(t, StoreConfig{
		SQLitePath:    filepath.Join(root, "basemap.db"),
		ScanRoot:      filepath.Join(root, "tiles"),
		APIBaseURL:    "http://127.0.0.1:8910",
		TileBaseURL:   "http://127.0.0.1:8910",
		VectorBaseURL: "http://127.0.0.1:8910",
	})
	job, err := store.CreateTileJob(ctx, CreateTileJobInput{SourcePath: source, TargetCode: "demo"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	found, err := store.GetTileJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if found.Code != job.Code {
		t.Fatalf("unexpected job %+v", found)
	}
	if _, err := store.GetTileJob(ctx, "nope"); err == nil {
		t.Fatal("expected a missing job to error")
	}
}

func TestTileJobCallbackFiresAndRetries(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	tileRoot := filepath.Join(root, "tiles")
	source := filepath.Join(root, "input.tif")
	writeTestFile(t, source, "fake raster")

	var attempts int
	received := make(chan map[string]any, 1)
	callback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		select {
		case received <- payload:
		default:
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer callback.Close()

	store := openTestStore(t, StoreConfig{
		SQLitePath:     filepath.Join(root, "basemap.db"),
		ScanRoot:       tileRoot,
		APIBaseURL:     "http://127.0.0.1:8910",
		TileBaseURL:    "http://127.0.0.1:8910",
		VectorBaseURL:  "http://127.0.0.1:8910",
		TileJobCommand: "definitely-not-a-real-binary {input} {output}",
	})
	if err := os.MkdirAll(tileRoot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	job, err := store.CreateTileJob(ctx, CreateTileJobInput{
		SourcePath:  source,
		TargetCode:  "demo",
		CallbackURL: callback.URL,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	store.runTileJob(ctx, job)

	// The first delivery fails, so the callback must be parked with a backoff.
	if err := store.dispatchTileJobCallbacks(ctx); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	jobs, err := store.ListTileJobs(ctx, 5)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if jobs[0].CallbackStatus != pushStatusRetrying {
		t.Fatalf("expected the callback to be retrying, got %q", jobs[0].CallbackStatus)
	}
	if jobs[0].CallbackNextAttemptAt == nil {
		t.Fatal("expected a next attempt to be scheduled")
	}

	if _, err := store.db.ExecContext(ctx, `UPDATE tile_jobs SET callback_next_attempt_at = ? WHERE id = ?`, toRFC3339(nowUTC().Add(-time.Minute)), job.ID); err != nil {
		t.Fatalf("force due: %v", err)
	}
	if err := store.dispatchTileJobCallbacks(ctx); err != nil {
		t.Fatalf("dispatch retry: %v", err)
	}

	jobs, _ = store.ListTileJobs(ctx, 5)
	if jobs[0].CallbackStatus != pushStatusSucceeded {
		t.Fatalf("expected the callback to succeed, got %q (%s)", jobs[0].CallbackStatus, jobs[0].CallbackMessage)
	}
	select {
	case payload := <-received:
		if payload["event"] != "tile.job.finished" {
			t.Fatalf("unexpected callback payload: %v", payload)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("callback payload never arrived")
	}
}

func TestTileJobCallbackSkippedWhenNoURL(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	tileRoot := filepath.Join(root, "tiles")
	source := filepath.Join(root, "input.tif")
	writeTestFile(t, source, "fake raster")
	if err := os.MkdirAll(tileRoot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	store := openTestStore(t, StoreConfig{
		SQLitePath:     filepath.Join(root, "basemap.db"),
		ScanRoot:       tileRoot,
		APIBaseURL:     "http://127.0.0.1:8910",
		TileBaseURL:    "http://127.0.0.1:8910",
		VectorBaseURL:  "http://127.0.0.1:8910",
		TileJobCommand: "definitely-not-a-real-binary {input} {output}",
	})
	job, err := store.CreateTileJob(ctx, CreateTileJobInput{SourcePath: source, TargetCode: "demo"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	store.runTileJob(ctx, job)

	jobs, _ := store.ListTileJobs(ctx, 5)
	if jobs[0].CallbackStatus != "" {
		t.Fatalf("expected no callback scheduling, got %q", jobs[0].CallbackStatus)
	}
}

func TestTileJobTemplateExpandsInputList(t *testing.T) {
	job := TileJob{
		SourcePaths: []string{`C:\a\in1.tif`, `C:\b\in2.tif`},
		TargetCode:  "demo",
		OutputRoot:  `C:\out`,
	}
	got := tileJobVRTCommandArgs(`gdalbuildvrt -resolution highest {output_vrt} {inputs}`, job, `C:\work\m.vrt`)
	want := []string{"gdalbuildvrt", "-resolution", "highest", `C:\work\m.vrt`, `C:\a\in1.tif`, `C:\b\in2.tif`}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("arg %d = %q, want %q", i, got[i], want[i])
		}
	}

	// The slicer still gets one input path (the VRT) when mosaicking.
	slicer := tileJobCommandArgs(`gdal2tiles.py --exclude {input} {output}`, job, `C:\work\m.vrt`)
	if slicer[2] != `C:\work\m.vrt` {
		t.Fatalf("expected the VRT to be the slicer input, got %v", slicer)
	}
}

func TestTileJobMosaicsMultipleSources(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the stub commands below use cmd.exe")
	}
	ctx := context.Background()
	root := t.TempDir()
	tileRoot := filepath.Join(root, "tiles")
	if err := os.MkdirAll(tileRoot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	first := filepath.Join(root, "flight-a.tif")
	second := filepath.Join(root, "flight-b.tif")
	writeTestFile(t, first, "fake raster a")
	writeTestFile(t, second, "fake raster b")

	store := openTestStore(t, StoreConfig{
		SQLitePath:    filepath.Join(root, "basemap.db"),
		ScanRoot:      tileRoot,
		APIBaseURL:    "http://127.0.0.1:8910",
		TileBaseURL:   "http://127.0.0.1:8910",
		VectorBaseURL: "http://127.0.0.1:8910",
		// Stand-ins: the mosaic step writes the VRT, the slicer lays down a z tree.
		TileJobVRTCommand: `cmd /c echo vrt > {output_vrt}`,
		TileJobCommand:    `cmd /c mkdir "{output}\0\0"`,
	})

	job, err := store.CreateTileJob(ctx, CreateTileJobInput{
		SourcePaths: []string{first, second},
		TargetCode:  "uav-2026",
		ZoomMin:     0,
		ZoomMax:     1,
	})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	if job.SourceCount != 2 || len(job.SourcePaths) != 2 {
		t.Fatalf("expected two sources on the job, got %d / %v", job.SourceCount, job.SourcePaths)
	}

	store.runTileJob(ctx, job)

	jobs, err := store.ListTileJobs(ctx, 5)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if jobs[0].Status != tileJobSucceeded {
		t.Fatalf("expected success, got %q (%s)", jobs[0].Status, jobs[0].Message)
	}
	if jobs[0].SourceCount != 2 {
		t.Fatalf("expected source_count 2 after a round trip, got %d", jobs[0].SourceCount)
	}

	// The mosaic step ran: its VRT sits beside the job log.
	vrt := filepath.Join(filepath.Dir(jobs[0].LogPath), job.Code+".vrt")
	if _, err := os.Stat(vrt); err != nil {
		t.Fatalf("expected the mosaic VRT at %s: %v", vrt, err)
	}

	// Both flights are recorded against the layer.
	sources, err := store.ListLayerSources(ctx, "uav-2026")
	if err != nil {
		t.Fatalf("list layer sources: %v", err)
	}
	if len(sources) != 2 {
		t.Fatalf("expected 2 recorded sources, got %d", len(sources))
	}

	// Adding the same image twice does not duplicate the registry entry.
	if err := store.AddLayerSources(ctx, "uav-2026", []string{first}); err != nil {
		t.Fatalf("re-add: %v", err)
	}
	sources, _ = store.ListLayerSources(ctx, "uav-2026")
	if len(sources) != 2 {
		t.Fatalf("expected re-adding to be a no-op, got %d entries", len(sources))
	}
}

func TestTileJobSingleSourceSkipsMosaic(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the stub command below uses cmd.exe")
	}
	ctx := context.Background()
	root := t.TempDir()
	tileRoot := filepath.Join(root, "tiles")
	if err := os.MkdirAll(tileRoot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	source := filepath.Join(root, "flight-a.tif")
	writeTestFile(t, source, "fake raster")

	store := openTestStore(t, StoreConfig{
		SQLitePath:    filepath.Join(root, "basemap.db"),
		ScanRoot:      tileRoot,
		APIBaseURL:    "http://127.0.0.1:8910",
		TileBaseURL:   "http://127.0.0.1:8910",
		VectorBaseURL: "http://127.0.0.1:8910",
		// A failing mosaic command proves the single-source path never calls it.
		TileJobVRTCommand: `definitely-not-a-real-binary {output_vrt} {inputs}`,
		TileJobCommand:    `cmd /c mkdir "{output}\0\0"`,
	})

	job, err := store.CreateTileJob(ctx, CreateTileJobInput{SourcePath: source, TargetCode: "uav-2026"})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	if job.SourceCount != 1 {
		t.Fatalf("expected source_count 1, got %d", job.SourceCount)
	}
	store.runTileJob(ctx, job)

	jobs, _ := store.ListTileJobs(ctx, 5)
	if jobs[0].Status != tileJobSucceeded {
		t.Fatalf("expected the single source to slice directly, got %q (%s)", jobs[0].Status, jobs[0].Message)
	}
}

func TestReadAllScopeCoversNewResourcesAndNeverExpires(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	tileRoot := filepath.Join(root, "tiles")
	vectorRoot := filepath.Join(root, "vectors")
	writeTestFile(t, filepath.Join(tileRoot, "first", "0", "0", "0.webp"), "tile")

	store := openTestStore(t, StoreConfig{
		SQLitePath:      filepath.Join(root, "basemap.db"),
		ScanRoot:        tileRoot,
		VectorScanRoot:  vectorRoot,
		APIBaseURL:      "http://127.0.0.1:8910",
		TileBaseURL:     "http://127.0.0.1:8910",
		VectorBaseURL:   "http://127.0.0.1:8910",
		TokenDefaultTTL: time.Hour,
	})
	if _, err := store.RunScan(ctx, defaultScanSourceCode); err != nil {
		t.Fatalf("scan: %v", err)
	}

	// No grants at all, and NoExpiry instead of the default hour.
	created, err := store.CreateToken(ctx, CreateTokenInput{
		Name:     "insar",
		Scopes:   []string{"read_all"},
		NoExpiry: true,
	})
	if err != nil {
		t.Fatalf("create read_all token: %v", err)
	}
	if created.Meta.ExpiresAt != nil {
		t.Fatalf("expected no expiry, got %v", created.Meta.ExpiresAt)
	}

	auth, err := store.AuthorizeToken(ctx, created.Token)
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if !auth.CanReadAll() {
		t.Fatal("expected the token to report blanket read access")
	}
	if !auth.CanReadBasemap("first", "current") {
		t.Fatal("expected read_all to reach an existing basemap")
	}

	// A basemap catalogued after the token was issued must be reachable too;
	// that is the whole point of read_all.
	writeTestFile(t, filepath.Join(tileRoot, "second", "0", "0", "0.webp"), "tile")
	if _, err := store.RunScan(ctx, defaultScanSourceCode); err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if !auth.CanReadBasemap("second", "current") {
		t.Fatal("expected read_all to cover a basemap added later")
	}

	// Vectors behave the same way.
	writeTestFile(t, filepath.Join(vectorRoot, "later.geojson"), `{"type":"FeatureCollection","features":[]}`)
	if _, err := store.RunVectorScan(ctx); err != nil {
		t.Fatalf("vector scan: %v", err)
	}
	assets, err := store.ListVectorAssets(ctx)
	if err != nil || len(assets) != 1 {
		t.Fatalf("expected one vector asset, got %d (%v)", len(assets), err)
	}
	if !auth.CanReadVector(assets[0].Code) {
		t.Fatal("expected read_all to cover a vector added later")
	}

	// A plain read token still needs grants, so the scope is not accidental.
	plain, err := store.CreateToken(ctx, CreateTokenInput{
		Name:         "plain",
		BasemapCodes: []string{"first"},
	})
	if err != nil {
		t.Fatalf("create plain token: %v", err)
	}
	plainAuth, err := store.AuthorizeToken(ctx, plain.Token)
	if err != nil {
		t.Fatalf("authorize plain: %v", err)
	}
	if plainAuth.CanReadBasemap("second", "current") {
		t.Fatal("a granted token must not pick up basemaps it was not granted")
	}
	if plainAuth.CanReadAll() {
		t.Fatal("a plain token must not report blanket access")
	}
}

func TestDeleteTokenRemovesGrants(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	tileRoot := filepath.Join(root, "tiles")
	writeTestFile(t, filepath.Join(tileRoot, "demo", "0", "0", "0.webp"), "tile")

	store := openTestStore(t, StoreConfig{
		SQLitePath:    filepath.Join(root, "basemap.db"),
		ScanRoot:      tileRoot,
		APIBaseURL:    "http://127.0.0.1:8910",
		TileBaseURL:   "http://127.0.0.1:8910",
		VectorBaseURL: "http://127.0.0.1:8910",
	})
	if _, err := store.RunScan(ctx, defaultScanSourceCode); err != nil {
		t.Fatalf("scan: %v", err)
	}

	created, err := store.CreateToken(ctx, CreateTokenInput{Name: "gone", BasemapCodes: []string{"demo"}})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	if err := store.DeleteToken(ctx, created.Meta.ID); err != nil {
		t.Fatalf("delete token: %v", err)
	}

	// The token itself is gone.
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM service_tokens WHERE id = ?`, created.Meta.ID).Scan(&count); err != nil {
		t.Fatalf("count tokens: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected the token row to be deleted, got %d", count)
	}

	// And so are its grants, via the foreign-key cascade.
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM token_grants WHERE token_id = ?`, created.Meta.ID).Scan(&count); err != nil {
		t.Fatalf("count grants: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected grants to cascade, got %d rows", count)
	}

	// The deleted token no longer authenticates.
	if _, err := store.AuthorizeToken(ctx, created.Token); err == nil {
		t.Fatal("a deleted token must not authenticate")
	}
	// Deleting an unknown id reports not-found.
	if err := store.DeleteToken(ctx, created.Meta.ID); err == nil {
		t.Fatal("expected deleting a missing token to error")
	}
}
