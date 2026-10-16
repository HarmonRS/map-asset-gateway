package basemap

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"tile-server/api-go/internal/uid"
)

// Tile job statuses.
const (
	tileJobPending   = "pending"
	tileJobRunning   = "running"
	tileJobSucceeded = "succeeded"
	tileJobFailed    = "failed"
)

// defaultTileJobCommand slices a raster into XYZ tiles. {input} is the source
// GeoTIFF, {output} the directory the gateway scans.
const defaultTileJobCommand = "gdal2tiles.py --xyz --zoom={zmin}-{zmax} --tilesize={tilesize} --exclude {input} {output}"

// defaultTileJobVRTCommand mosaics a layer's sources into one virtual raster
// so overlapping flights composite per pixel instead of per tile.
const defaultTileJobVRTCommand = "gdalbuildvrt -resolution highest {output_vrt} {inputs}"

// tileJobColumns keeps every read path in step with the struct.
const tileJobColumns = `id, code, name, source_path, source_count, sources_json, target_code, output_root, zoom_min, zoom_max,
	tile_size, tile_format, status, progress, message, log_path, callback_url, callback_status,
	callback_attempts, callback_next_attempt_at, callback_message, requested_by, created_at, started_at, finished_at`

type TileJob struct {
	ID                    string     `json:"id"`
	Code                  string     `json:"code"`
	Name                  string     `json:"name"`
	SourcePath            string     `json:"source_path"`
	SourceCount           int        `json:"source_count"`
	SourcePaths           []string   `json:"source_paths,omitempty"`
	TargetCode            string     `json:"target_code"`
	OutputRoot            string     `json:"output_root"`
	ZoomMin               int        `json:"zoom_min"`
	ZoomMax               int        `json:"zoom_max"`
	TileSize              int        `json:"tile_size"`
	TileFormat            string     `json:"tile_format"`
	Status                string     `json:"status"`
	Progress              string     `json:"progress"`
	Message               string     `json:"message"`
	LogPath               string     `json:"log_path"`
	CallbackURL           string     `json:"callback_url,omitempty"`
	CallbackStatus        string     `json:"callback_status,omitempty"`
	CallbackAttempts      int        `json:"callback_attempts,omitempty"`
	CallbackMessage       string     `json:"callback_message,omitempty"`
	CallbackNextAttemptAt *time.Time `json:"callback_next_attempt_at,omitempty"`
	RequestedBy           string     `json:"requested_by"`
	CreatedAt             time.Time  `json:"created_at"`
	StartedAt             *time.Time `json:"started_at,omitempty"`
	FinishedAt            *time.Time `json:"finished_at,omitempty"`
}

type CreateTileJobInput struct {
	Code        string   `json:"code"`
	Name        string   `json:"name"`
	SourcePath  string   `json:"source_path"`
	SourcePaths []string `json:"source_paths"`
	TargetCode  string   `json:"target_code"`
	ZoomMin     int      `json:"zoom_min"`
	ZoomMax     int      `json:"zoom_max"`
	TileSize    int      `json:"tile_size"`
	TileFormat  string   `json:"tile_format"`
	CallbackURL string   `json:"callback_url"`
	RequestedBy string   `json:"requested_by"`
}

// CreateTileJob validates the request and queues it. Slicing itself runs in the
// background so a long gdal2tiles run cannot block the console.
func (s *Store) CreateTileJob(ctx context.Context, input CreateTileJobInput) (TileJob, error) {
	sources, err := resolveTileJobSources(input)
	if err != nil {
		return TileJob{}, err
	}
	absSource := sources[0]

	targetCode := normalizeBasemapCode(input.TargetCode)
	if targetCode == "" {
		return TileJob{}, errors.New("target code is required")
	}
	scanRoot := strings.TrimSpace(s.cfg.ScanRoot)
	if scanRoot == "" {
		return TileJob{}, errors.New("basemap scan root is not configured")
	}
	outputRoot := filepath.Join(scanRoot, targetCode)

	if input.ZoomMin < 0 || input.ZoomMax < 0 || input.ZoomMax < input.ZoomMin {
		return TileJob{}, errors.New("invalid zoom range")
	}
	if input.ZoomMax > 24 {
		return TileJob{}, errors.New("zoom must be 24 or lower")
	}
	tileSize := input.TileSize
	if tileSize <= 0 {
		tileSize = 256
	}
	tileFormat := strings.ToLower(strings.TrimSpace(input.TileFormat))
	if tileFormat == "" {
		tileFormat = "webp"
	}
	code := normalizeBasemapCode(input.Code)
	if code == "" {
		code = normalizeBasemapCode(targetCode + "-" + nowUTC().Format("20060102150405"))
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = filepath.Base(absSource)
	}

	now := nowUTC()
	job := TileJob{
		ID:          uid.New(),
		Code:        code,
		Name:        name,
		SourcePath:  absSource,
		SourceCount: len(sources),
		SourcePaths: sources,
		TargetCode:  targetCode,
		OutputRoot:  outputRoot,
		ZoomMin:     input.ZoomMin,
		ZoomMax:     input.ZoomMax,
		TileSize:    tileSize,
		TileFormat:  tileFormat,
		Status:      tileJobPending,
		RequestedBy: strings.TrimSpace(input.RequestedBy),
		CreatedAt:   now,
	}
	job.CallbackURL = strings.TrimSpace(input.CallbackURL)

	// A caller-supplied code makes submission idempotent: sending the same code
	// again returns the job that already exists instead of queueing a second copy.
	if existing, ok, err := s.getTileJobByCode(ctx, job.Code); err != nil {
		return TileJob{}, err
	} else if ok {
		return existing, nil
	}

	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO tile_jobs (
			id, code, name, source_path, source_count, sources_json, target_code, output_root, zoom_min, zoom_max,
			tile_size, tile_format, status, progress, message, log_path, callback_url, requested_by, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', '', '', ?, ?, ?)
	`, job.ID, job.Code, job.Name, job.SourcePath, job.SourceCount, writeJSON(job.SourcePaths), job.TargetCode, job.OutputRoot,
		job.ZoomMin, job.ZoomMax, job.TileSize, job.TileFormat, job.Status,
		job.CallbackURL, job.RequestedBy, toRFC3339(now)); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			if existing, ok, getErr := s.getTileJobByCode(ctx, job.Code); getErr == nil && ok {
				return existing, nil
			}
		}
		return TileJob{}, err
	}
	// Remember what this layer is made of so it can be rebuilt later.
	if err := s.AddLayerSources(ctx, job.TargetCode, sources); err != nil {
		log.Printf("record layer sources for %s: %v", job.TargetCode, err)
	}

	return job, nil
}

func (s *Store) ListTileJobs(ctx context.Context, limit int) ([]TileJob, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+tileJobColumns+`
		FROM tile_jobs
		ORDER BY created_at DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []TileJob
	for rows.Next() {
		job, err := scanTileJob(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, job)
	}
	return items, rows.Err()
}

func scanTileJob(scanner interface {
	Scan(dest ...any) error
}) (TileJob, error) {
	var job TileJob
	var createdAt string
	var sourcesJSON string
	var startedAt, finishedAt, callbackNextAttemptAt sql.NullString
	if err := scanner.Scan(
		&job.ID, &job.Code, &job.Name, &job.SourcePath, &job.SourceCount, &sourcesJSON, &job.TargetCode, &job.OutputRoot,
		&job.ZoomMin, &job.ZoomMax, &job.TileSize, &job.TileFormat, &job.Status,
		&job.Progress, &job.Message, &job.LogPath, &job.CallbackURL, &job.CallbackStatus, &job.CallbackAttempts,
		&callbackNextAttemptAt, &job.CallbackMessage, &job.RequestedBy, &createdAt,
		&startedAt, &finishedAt,
	); err != nil {
		return TileJob{}, err
	}
	job.CreatedAt = parseTime(createdAt)
	if strings.TrimSpace(sourcesJSON) != "" {
		_ = readMaybeJSON(sourcesJSON, &job.SourcePaths)
	}
	if len(job.SourcePaths) == 0 && job.SourcePath != "" {
		job.SourcePaths = []string{job.SourcePath}
	}
	if startedAt.Valid {
		value := parseTime(startedAt.String)
		job.StartedAt = &value
	}
	if finishedAt.Valid {
		value := parseTime(finishedAt.String)
		job.FinishedAt = &value
	}
	if callbackNextAttemptAt.Valid {
		value := parseTime(callbackNextAttemptAt.String)
		job.CallbackNextAttemptAt = &value
	}
	return job, nil
}

// GetTileJob returns one job by id so a caller can poll a submission.
func (s *Store) GetTileJob(ctx context.Context, id string) (TileJob, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+tileJobColumns+" FROM tile_jobs WHERE id = ?", strings.TrimSpace(id))
	job, err := scanTileJob(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TileJob{}, fmt.Errorf("tile job %q not found", id)
		}
		return TileJob{}, err
	}
	return job, nil
}

func (s *Store) getTileJobByCode(ctx context.Context, code string) (TileJob, bool, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+tileJobColumns+" FROM tile_jobs WHERE code = ?", code)
	job, err := scanTileJob(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TileJob{}, false, nil
		}
		return TileJob{}, false, err
	}
	return job, true, nil
}

// runTileJob executes one queued job and records the outcome verbatim.
func (s *Store) runTileJob(ctx context.Context, job TileJob) {
	startedAt := nowUTC()
	if _, err := s.db.ExecContext(ctx, `
		UPDATE tile_jobs SET status = ?, started_at = ?, message = '' WHERE id = ?
	`, tileJobRunning, toRFC3339(startedAt), job.ID); err != nil {
		return
	}

	logPath, _, runErr := s.executeTileJob(ctx, job)

	status := tileJobSucceeded
	message := ""
	if runErr != nil {
		status = tileJobFailed
		message = runErr.Error()
	}
	finishedAt := nowUTC()
	if _, err := s.db.ExecContext(context.Background(), `
		UPDATE tile_jobs SET status = ?, message = ?, log_path = ?, progress = ?, finished_at = ? WHERE id = ?
	`, status, message, logPath, message, toRFC3339(finishedAt), job.ID); err != nil {
		return
	}
	// Tell the submitting system the job is done, if it asked to be told.
	if strings.TrimSpace(job.CallbackURL) != "" {
		s.queueTileJobCallback(context.Background(), job.ID)
	}

	// A sliced basemap only becomes visible after the scan picks it up.
	if status == tileJobSucceeded && strings.TrimSpace(s.cfg.ScanRoot) != "" {
		if _, err := s.RunScan(context.Background(), defaultScanSourceCode); err != nil {
			if _, updateErr := s.db.ExecContext(context.Background(), `
				UPDATE tile_jobs SET message = ? WHERE id = ?
			`, "切片完成，但扫描入库失败："+err.Error(), job.ID); updateErr != nil {
				return
			}
		}
	}
}

// executeTileJob builds the input (mosaicking when several sources were
// submitted), runs the slicer and captures its output.
func (s *Store) executeTileJob(ctx context.Context, job TileJob) (string, []string, error) {
	if err := os.MkdirAll(job.OutputRoot, 0o755); err != nil {
		return "", nil, fmt.Errorf(
			"create output root %s: %w (the account running the service needs write access to the basemap scan root)",
			job.OutputRoot, err)
	}
	logDir := strings.TrimSpace(s.cfg.TileJobLogDir)
	if logDir == "" {
		// Keep logs beside the data: a system temp directory is not always
		// writable for the service account, and this stays under the scan root
		// without introducing a zoom directory the scanner would pick up.
		logDir = filepath.Join(filepath.Dir(job.OutputRoot), ".tile-jobs")
	}
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return "", nil, fmt.Errorf("create log dir: %w", err)
	}
	logPath := filepath.Join(logDir, job.Code+".log")
	logFile, err := os.Create(logPath)
	if err != nil {
		return logPath, nil, fmt.Errorf("create job log: %w", err)
	}
	defer logFile.Close()

	// One source slices directly; several are mosaicked first so overlapping
	// flights composite per pixel instead of per tile.
	input := job.SourcePath
	if len(job.SourcePaths) > 1 {
		vrtPath := filepath.Join(logDir, job.Code+".vrt")
		if err := s.buildTileJobMosaic(ctx, job, vrtPath, logFile); err != nil {
			return logPath, nil, err
		}
		input = vrtPath
	}

	template := strings.TrimSpace(s.cfg.TileJobCommand)
	if template == "" {
		template = defaultTileJobCommand
	}
	resolved := tileJobCommandArgs(template, job, input)
	if len(resolved) == 0 {
		return logPath, nil, errors.New("tile job command template is empty")
	}

	command := exec.CommandContext(ctx, resolved[0], resolved[1:]...)
	command.Stdout = logFile
	command.Stderr = logFile
	if err := command.Run(); err != nil {
		return logPath, resolved, fmt.Errorf("%s: %w", strings.Join(resolved, " "), err)
	}
	return logPath, resolved, nil
}

// buildTileJobMosaic runs gdalbuildvrt over the job's sources.
func (s *Store) buildTileJobMosaic(ctx context.Context, job TileJob, vrtPath string, logFile *os.File) error {
	template := strings.TrimSpace(s.cfg.TileJobVRTCommand)
	if template == "" {
		template = defaultTileJobVRTCommand
	}
	resolved := tileJobVRTCommandArgs(template, job, vrtPath)
	if len(resolved) == 0 {
		return errors.New("tile job vrt command template is empty")
	}
	command := exec.CommandContext(ctx, resolved[0], resolved[1:]...)
	command.Stdout = logFile
	command.Stderr = logFile
	if err := command.Run(); err != nil {
		return fmt.Errorf("build mosaic: %s: %w", strings.Join(resolved, " "), err)
	}
	return nil
}

// resolveTileJobSources validates every submitted source and returns absolute
// paths. SourcePaths wins over the single SourcePath field.
func resolveTileJobSources(input CreateTileJobInput) ([]string, error) {
	raw := input.SourcePaths
	if len(raw) == 0 && strings.TrimSpace(input.SourcePath) != "" {
		raw = []string{input.SourcePath}
	}
	if len(raw) == 0 {
		return nil, errors.New("source path is required")
	}

	seen := map[string]struct{}{}
	sources := make([]string, 0, len(raw))
	for _, candidate := range raw {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		abs, err := filepath.Abs(candidate)
		if err != nil {
			return nil, fmt.Errorf("resolve source path %q: %w", candidate, err)
		}
		info, err := os.Stat(abs)
		if err != nil {
			return nil, fmt.Errorf("source path %q is not readable: %w", candidate, err)
		}
		if info.IsDir() {
			return nil, fmt.Errorf("source path %q must be a file, not a directory", candidate)
		}
		if _, dup := seen[abs]; dup {
			continue
		}
		seen[abs] = struct{}{}
		sources = append(sources, abs)
	}
	if len(sources) == 0 {
		return nil, errors.New("source path is required")
	}
	return sources, nil
}

// tileJobCommandArgs resolves the slicer template. Quoted segments are kept
// together so paths with spaces survive.
func tileJobCommandArgs(template string, job TileJob, input string) []string {
	return expandTileJobTemplate(template, map[string]string{
		"{input}":    input,
		"{output}":   job.OutputRoot,
		"{zmin}":     fmt.Sprintf("%d", job.ZoomMin),
		"{zmax}":     fmt.Sprintf("%d", job.ZoomMax),
		"{tilesize}": fmt.Sprintf("%d", job.TileSize),
		"{format}":   job.TileFormat,
		"{code}":     job.TargetCode,
	}, nil)
}

// tileJobVRTCommandArgs resolves the mosaic template. {inputs} expands into one
// argument per source raster.
func tileJobVRTCommandArgs(template string, job TileJob, vrtPath string) []string {
	return expandTileJobTemplate(template, map[string]string{
		"{output_vrt}": vrtPath,
		"{output}":     job.OutputRoot,
		"{code}":       job.TargetCode,
	}, map[string][]string{"{inputs}": job.SourcePaths})
}

// expandTileJobTemplate splits the template and substitutes placeholders. A
// token that is exactly a list placeholder expands into several arguments.
func expandTileJobTemplate(template string, scalars map[string]string, lists map[string][]string) []string {
	parts := splitCommandLine(template)
	args := make([]string, 0, len(parts))
	for _, part := range parts {
		if values, ok := lists[part]; ok {
			args = append(args, values...)
			continue
		}
		for key, value := range scalars {
			part = strings.ReplaceAll(part, key, value)
		}
		args = append(args, part)
	}
	return args
}

// splitCommandLine splits on whitespace while honouring double quotes.
func splitCommandLine(value string) []string {
	var parts []string
	var current strings.Builder
	inQuotes := false
	for _, r := range value {
		switch {
		case r == '"':
			inQuotes = !inQuotes
		case (r == ' ' || r == '\t' || r == '\n' || r == '\r') && !inQuotes:
			if current.Len() > 0 {
				parts = append(parts, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}
	if current.Len() > 0 {
		parts = append(parts, current.String())
	}
	return parts
}

// dispatchTileJobs runs queued jobs one at a time, oldest first.
func (s *Store) dispatchTileJobs(ctx context.Context) {
	for {
		job, ok, err := s.nextPendingTileJob(ctx)
		if err != nil || !ok {
			return
		}
		s.runTileJob(ctx, job)
	}
}

func (s *Store) nextPendingTileJob(ctx context.Context) (TileJob, bool, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+tileJobColumns+`
		FROM tile_jobs
		WHERE status = ?
		ORDER BY created_at
		LIMIT 1
	`, tileJobPending)
	job, err := scanTileJob(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TileJob{}, false, nil
		}
		return TileJob{}, false, err
	}
	return job, true, nil
}

// Callback delivery for tile jobs. A finished job can notify the submitting
// system; failures back off on the same schedule as push callbacks.
const tileCallbackMaxAttempts = 3

func (s *Store) queueTileJobCallback(ctx context.Context, jobID string) {
	if _, err := s.db.ExecContext(ctx, `
		UPDATE tile_jobs
		SET callback_status = ?, callback_attempts = 0, callback_next_attempt_at = ?, callback_message = ''
		WHERE id = ?
	`, pushStatusRetrying, toRFC3339(nowUTC()), jobID); err != nil {
		log.Printf("queue tile job callback %s: %v", jobID, err)
	}
}

func (s *Store) dispatchTileJobCallbacks(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, "SELECT "+tileJobColumns+`
		FROM tile_jobs
		WHERE callback_url <> '' AND callback_status = ?
			AND (callback_next_attempt_at IS NULL OR callback_next_attempt_at <= ?)
		ORDER BY callback_next_attempt_at
		LIMIT 5
	`, pushStatusRetrying, toRFC3339(nowUTC()))
	if err != nil {
		return err
	}
	defer rows.Close()

	var jobs []TileJob
	for rows.Next() {
		job, err := scanTileJob(rows)
		if err != nil {
			return err
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, job := range jobs {
		s.attemptTileJobCallback(job, job.CallbackAttempts+1)
	}
	return nil
}

func (s *Store) attemptTileJobCallback(job TileJob, attempt int) {
	ctx, cancel := context.WithTimeout(context.Background(), pushCallbackTimeout)
	defer cancel()

	payload := map[string]any{
		"event":     "tile.job.finished",
		"job":       job,
		"delivered": toRFC3339(nowUTC()),
	}
	_, _, err := s.postJSON(ctx, http.MethodPost, job.CallbackURL, nil, payload)
	message := ""
	if err != nil {
		message = err.Error()
	}

	if err != nil && attempt < tileCallbackMaxAttempts {
		next := nowUTC().Add(pushRetryDelay(attempt))
		if _, updateErr := s.db.ExecContext(context.Background(), `
			UPDATE tile_jobs
			SET callback_status = ?, callback_attempts = ?, callback_next_attempt_at = ?, callback_message = ?
			WHERE id = ?
		`, pushStatusRetrying, attempt, toRFC3339(next), message, job.ID); updateErr != nil {
			log.Printf("reschedule tile job callback %s: %v", job.ID, updateErr)
		}
		return
	}

	status := pushStatusSucceeded
	if err != nil {
		status = pushStatusFailed
	}
	if _, updateErr := s.db.ExecContext(context.Background(), `
		UPDATE tile_jobs
		SET callback_status = ?, callback_attempts = ?, callback_next_attempt_at = NULL, callback_message = ?
		WHERE id = ?
	`, status, attempt, message, job.ID); updateErr != nil {
		log.Printf("record tile job callback %s: %v", job.ID, updateErr)
	}
}

// LayerSource records one raster that belongs to a layer. The layer's tile
// directory is the union of its sources, so adding a source and re-slicing
// accumulates imagery instead of replacing the layer.
type LayerSource struct {
	ID         string    `json:"id"`
	TargetCode string    `json:"target_code"`
	SourcePath string    `json:"source_path"`
	AddedAt    time.Time `json:"added_at"`
}

// AddLayerSources records sources against a layer. Re-adding a known path is a
// no-op, which keeps repeated submissions idempotent.
func (s *Store) AddLayerSources(ctx context.Context, targetCode string, paths []string) error {
	code := normalizeBasemapCode(targetCode)
	if code == "" {
		return errors.New("target code is required")
	}
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if _, err := s.db.ExecContext(ctx, `
			INSERT INTO layer_sources (id, target_code, source_path, added_at)
			VALUES (?, ?, ?, ?)
			ON CONFLICT(target_code, source_path) DO NOTHING
		`, uid.New(), code, path, toRFC3339(nowUTC())); err != nil {
			return err
		}
	}
	return nil
}

// ListLayerSources returns a layer's source inventory in the order it was added.
func (s *Store) ListLayerSources(ctx context.Context, targetCode string) ([]LayerSource, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, target_code, source_path, added_at
		FROM layer_sources
		WHERE target_code = ?
		ORDER BY added_at
	`, normalizeBasemapCode(targetCode))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []LayerSource
	for rows.Next() {
		var item LayerSource
		var addedAt string
		if err := rows.Scan(&item.ID, &item.TargetCode, &item.SourcePath, &addedAt); err != nil {
			return nil, err
		}
		item.AddedAt = parseTime(addedAt)
		items = append(items, item)
	}
	return items, rows.Err()
}

// ListLayerInventories returns every layer with its sources, ordered by layer
// code so the console renders stably.
func (s *Store) ListLayerInventories(ctx context.Context) ([]LayerInventory, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, target_code, source_path, added_at
		FROM layer_sources
		ORDER BY target_code, added_at
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byCode := map[string][]LayerSource{}
	var codes []string
	for rows.Next() {
		var item LayerSource
		var addedAt string
		if err := rows.Scan(&item.ID, &item.TargetCode, &item.SourcePath, &addedAt); err != nil {
			return nil, err
		}
		item.AddedAt = parseTime(addedAt)
		if _, seen := byCode[item.TargetCode]; !seen {
			codes = append(codes, item.TargetCode)
		}
		byCode[item.TargetCode] = append(byCode[item.TargetCode], item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	items := make([]LayerInventory, 0, len(codes))
	for _, code := range codes {
		item := LayerInventory{TargetCode: code, Sources: byCode[code], ZoomMin: 0, ZoomMax: 18, TileFormat: "webp"}
		if minZoom, maxZoom, format, ok := s.layerPublishedRange(ctx, code); ok {
			item.ZoomMin, item.ZoomMax, item.TileFormat = minZoom, maxZoom, format
		}
		items = append(items, item)
	}
	return items, nil
}

// layerPublishedRange returns the zoom range and format the layer is currently
// published at, so a rebuild keeps the same range instead of guessing.
func (s *Store) layerPublishedRange(ctx context.Context, targetCode string) (int, int, string, bool) {
	var minZoom, maxZoom int
	var format string
	err := s.db.QueryRowContext(ctx, `
		SELECT v.min_zoom, v.max_zoom, v.tile_format
		FROM basemap_versions v
		JOIN basemaps b ON b.id = v.basemap_id
		WHERE b.code = ?
		ORDER BY v.is_default DESC, v.version
		LIMIT 1
	`, normalizeBasemapCode(targetCode)).Scan(&minZoom, &maxZoom, &format)
	if err != nil {
		return 0, 0, "", false
	}
	return minZoom, maxZoom, format, true
}
