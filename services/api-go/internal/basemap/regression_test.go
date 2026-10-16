package basemap

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func openTestStore(t *testing.T, cfg StoreConfig) *Store {
	t.Helper()
	store, err := OpenStore(cfg)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
	})
	return store
}

func TestRunScanRefusesConcurrentRuns(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	tileRoot := filepath.Join(root, "tiles")
	writeTestFile(t, filepath.Join(tileRoot, "google_image", "0", "0", "0.webp"), "tile")
	vectorRoot := filepath.Join(root, "vectors")

	store := openTestStore(t, StoreConfig{
		SQLitePath:     filepath.Join(root, "basemap.db"),
		ScanRoot:       tileRoot,
		VectorScanRoot: vectorRoot,
		APIBaseURL:     "http://127.0.0.1:8910",
		TileBaseURL:    "http://127.0.0.1:8910",
		VectorBaseURL:  "http://127.0.0.1:8910",
	})

	store.scanMu.Lock()
	if _, err := store.RunScan(ctx, defaultScanSourceCode); !errors.Is(err, ErrScanInProgress) {
		store.scanMu.Unlock()
		t.Fatalf("expected ErrScanInProgress, got %v", err)
	}
	if _, err := store.RunVectorScan(ctx); !errors.Is(err, ErrScanInProgress) {
		store.scanMu.Unlock()
		t.Fatalf("expected ErrScanInProgress from vector scan, got %v", err)
	}
	store.scanMu.Unlock()

	if _, err := store.RunScan(ctx, defaultScanSourceCode); err != nil {
		t.Fatalf("run scan after unlock: %v", err)
	}
}

func TestRunScanReportsUnknownSource(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store := openTestStore(t, StoreConfig{SQLitePath: filepath.Join(root, "basemap.db")})

	if _, err := store.RunScan(ctx, "does-not-exist"); !errors.Is(err, ErrScanSourceNotFound) {
		t.Fatalf("expected ErrScanSourceNotFound, got %v", err)
	}
}

func TestRescanOnlyCountsRealChanges(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	tileRoot := filepath.Join(root, "tiles")
	writeTestFile(t, filepath.Join(tileRoot, "google_image", "0", "0", "0.webp"), "tile")

	store := openTestStore(t, StoreConfig{
		SQLitePath:    filepath.Join(root, "basemap.db"),
		ScanRoot:      tileRoot,
		APIBaseURL:    "http://127.0.0.1:8910",
		TileBaseURL:   "http://127.0.0.1:8910",
		VectorBaseURL: "http://127.0.0.1:8910",
	})

	first, err := store.RunScan(ctx, defaultScanSourceCode)
	if err != nil {
		t.Fatalf("first scan: %v", err)
	}
	if first.AddedCount != 1 {
		t.Fatalf("expected 1 added on first scan, got %d", first.AddedCount)
	}

	second, err := store.RunScan(ctx, defaultScanSourceCode)
	if err != nil {
		t.Fatalf("second scan: %v", err)
	}
	if second.UpdatedCount != 0 {
		t.Fatalf("expected 0 updates on an unchanged rescan, got %d", second.UpdatedCount)
	}
	if second.AddedCount != 0 || second.RemovedCount != 0 {
		t.Fatalf("expected no additions/removals, got +%d/-%d", second.AddedCount, second.RemovedCount)
	}
	if got := second.Summary["unchanged_count"]; got != 1 {
		t.Fatalf("expected unchanged_count=1, got %v", got)
	}

	// A new zoom level is a genuine change and must be reported as an update.
	writeTestFile(t, filepath.Join(tileRoot, "google_image", "1", "0", "0.webp"), "tile")
	third, err := store.RunScan(ctx, defaultScanSourceCode)
	if err != nil {
		t.Fatalf("third scan: %v", err)
	}
	if third.UpdatedCount != 1 {
		t.Fatalf("expected 1 update after adding a zoom level, got %d", third.UpdatedCount)
	}
}

func TestVectorCodeCollisionKeepsBothAssets(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	vectorRoot := filepath.Join(root, "vectors")
	writeTestFile(t, filepath.Join(vectorRoot, "a", "b.geojson"), `{"type":"FeatureCollection","features":[]}`)
	writeTestFile(t, filepath.Join(vectorRoot, "a-b.geojson"), `{"type":"FeatureCollection","features":[]}`)

	store := openTestStore(t, StoreConfig{
		SQLitePath:     filepath.Join(root, "basemap.db"),
		VectorScanRoot: vectorRoot,
		APIBaseURL:     "http://127.0.0.1:8910",
		VectorBaseURL:  "http://127.0.0.1:8910",
	})

	if _, err := store.RunVectorScan(ctx); err != nil {
		t.Fatalf("first vector scan: %v", err)
	}
	assets, err := store.ListVectorAssets(ctx)
	if err != nil {
		t.Fatalf("list assets: %v", err)
	}
	if len(assets) != 2 {
		t.Fatalf("expected both colliding assets to survive, got %d", len(assets))
	}
	if assets[0].Code == assets[1].Code {
		t.Fatalf("expected distinct codes, both are %q", assets[0].Code)
	}
	firstCodes := []string{assets[0].Code, assets[1].Code}

	if _, err := store.RunVectorScan(ctx); err != nil {
		t.Fatalf("second vector scan: %v", err)
	}
	assets, err = store.ListVectorAssets(ctx)
	if err != nil {
		t.Fatalf("list assets after rescan: %v", err)
	}
	if len(assets) != 2 {
		t.Fatalf("rescan dropped an asset, now %d", len(assets))
	}
	secondCodes := map[string]bool{assets[0].Code: true, assets[1].Code: true}
	for _, code := range firstCodes {
		if !secondCodes[code] {
			t.Fatalf("disambiguated code %q was not stable across rescans", code)
		}
	}
}

func TestVectorScanReusesChecksumUntilFileChanges(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	vectorRoot := filepath.Join(root, "vectors")
	file := filepath.Join(vectorRoot, "boundary.geojson")
	writeTestFile(t, file, `{"type":"FeatureCollection","features":[]}`)

	store := openTestStore(t, StoreConfig{
		SQLitePath:     filepath.Join(root, "basemap.db"),
		VectorScanRoot: vectorRoot,
		APIBaseURL:     "http://127.0.0.1:8910",
		VectorBaseURL:  "http://127.0.0.1:8910",
	})

	if _, err := store.RunVectorScan(ctx); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	first, err := store.GetVectorAssetByCode(ctx, "boundary")
	if err != nil {
		t.Fatalf("get asset: %v", err)
	}
	if first.Metadata["hash_size"] == nil || first.Metadata["hash_mtime"] == nil {
		t.Fatalf("expected hash stamp in metadata, got %v", first.Metadata)
	}

	second, err := store.RunVectorScan(ctx)
	if err != nil {
		t.Fatalf("second scan: %v", err)
	}
	if second["unchanged_count"] != 1 {
		t.Fatalf("expected unchanged_count=1, got %v", second["unchanged_count"])
	}
	reused, err := store.GetVectorAssetByCode(ctx, "boundary")
	if err != nil {
		t.Fatalf("get asset after rescan: %v", err)
	}
	if reused.Checksum != first.Checksum {
		t.Fatalf("checksum changed without a file change: %q -> %q", first.Checksum, reused.Checksum)
	}

	// Rewriting the file with different content must invalidate the cached hash.
	writeTestFile(t, file, `{"type":"FeatureCollection","features":[{"type":"Feature"}]}`)
	if _, err := store.RunVectorScan(ctx); err != nil {
		t.Fatalf("third scan: %v", err)
	}
	changed, err := store.GetVectorAssetByCode(ctx, "boundary")
	if err != nil {
		t.Fatalf("get asset after change: %v", err)
	}
	if changed.Checksum == first.Checksum {
		t.Fatal("expected checksum to change after the file changed")
	}
}

func TestCreateTokenAppliesDefaultTTL(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	tileRoot := filepath.Join(root, "tiles")
	writeTestFile(t, filepath.Join(tileRoot, "google_image", "0", "0", "0.webp"), "tile")

	store := openTestStore(t, StoreConfig{
		SQLitePath:      filepath.Join(root, "basemap.db"),
		ScanRoot:        tileRoot,
		APIBaseURL:      "http://127.0.0.1:8910",
		TileBaseURL:     "http://127.0.0.1:8910",
		VectorBaseURL:   "http://127.0.0.1:8910",
		TokenDefaultTTL: time.Hour,
	})
	if _, err := store.RunScan(ctx, defaultScanSourceCode); err != nil {
		t.Fatalf("run scan: %v", err)
	}

	created, err := store.CreateToken(ctx, CreateTokenInput{Name: "ttl", BasemapCodes: []string{"google_image"}})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	if created.Meta.ExpiresAt == nil {
		t.Fatal("expected a default expiry to be applied")
	}
	remaining := time.Until(*created.Meta.ExpiresAt)
	if remaining < 55*time.Minute || remaining > 65*time.Minute {
		t.Fatalf("expected ~1h expiry, got %s", remaining)
	}
}

func TestPushRunsAsynchronously(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	tileRoot := filepath.Join(root, "tiles")
	writeTestFile(t, filepath.Join(tileRoot, "google_image", "0", "0", "0.webp"), "tile")

	received := make(chan map[string]any, 1)
	callback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		SQLitePath:    filepath.Join(root, "basemap.db"),
		ScanRoot:      tileRoot,
		APIBaseURL:    "http://127.0.0.1:8910",
		TileBaseURL:   "http://127.0.0.1:8910",
		VectorBaseURL: "http://127.0.0.1:8910",
	})
	if _, err := store.RunScan(ctx, defaultScanSourceCode); err != nil {
		t.Fatalf("run scan: %v", err)
	}
	if _, err := store.CreateTargetSystem(ctx, CreateTargetSystemInput{
		Code:        "downstream",
		CallbackURL: callback.URL,
	}); err != nil {
		t.Fatalf("create target: %v", err)
	}

	record, err := store.PushBasemapVersion(ctx, "downstream", "google_image", "current")
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if record.Status != "pending" {
		t.Fatalf("expected the push to be queued as pending, got %q", record.Status)
	}

	payload := waitForPayload(t, received)
	if payload["event"] != "basemap.published" {
		t.Fatalf("unexpected callback payload: %v", payload)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		records, err := store.ListPushRecords(ctx, 5)
		if err != nil {
			t.Fatalf("list push records: %v", err)
		}
		done := false
		for _, item := range records {
			if item.ID == record.ID && item.Status == "succeeded" {
				done = true
			}
		}
		if done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("push record %s never reached succeeded: %+v", record.ID, records)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func waitForPayload(t *testing.T, received <-chan map[string]any) map[string]any {
	t.Helper()
	select {
	case payload := <-received:
		return payload
	case <-time.After(5 * time.Second):
		t.Fatal("callback was never invoked")
		return nil
	}
}

func TestCloseWaitsForQueuedPush(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	tileRoot := filepath.Join(root, "tiles")
	writeTestFile(t, filepath.Join(tileRoot, "google_image", "0", "0", "0.webp"), "tile")

	started := make(chan struct{}, 1)
	release := make(chan struct{})
	callback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	defer callback.Close()

	store, err := OpenStore(StoreConfig{
		SQLitePath:    filepath.Join(root, "basemap.db"),
		ScanRoot:      tileRoot,
		APIBaseURL:    "http://127.0.0.1:8910",
		TileBaseURL:   "http://127.0.0.1:8910",
		VectorBaseURL: "http://127.0.0.1:8910",
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if _, err := store.RunScan(ctx, defaultScanSourceCode); err != nil {
		t.Fatalf("run scan: %v", err)
	}
	if _, err := store.CreateTargetSystem(ctx, CreateTargetSystemInput{Code: "downstream", CallbackURL: callback.URL}); err != nil {
		t.Fatalf("create target: %v", err)
	}
	if _, err := store.PushBasemapVersion(ctx, "downstream", "google_image", "current"); err != nil {
		t.Fatalf("push: %v", err)
	}

	<-started
	closed := make(chan struct{})
	go func() {
		_ = store.Close()
		close(closed)
	}()
	select {
	case <-closed:
		t.Fatal("Close returned while a push callback was still in flight")
	case <-time.After(150 * time.Millisecond):
	}
	close(release)
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return after the push finished")
	}
}

func TestScanRootDirectoryIsNotRequiredAtOpen(t *testing.T) {
	root := t.TempDir()
	store := openTestStore(t, StoreConfig{SQLitePath: filepath.Join(root, "basemap.db")})
	if store.ScanRoot() != "" {
		t.Fatalf("expected empty scan root, got %q", store.ScanRoot())
	}
	if _, err := os.Stat(filepath.Join(root, "basemap.db")); err != nil {
		t.Fatalf("expected the database file to exist: %v", err)
	}
}
