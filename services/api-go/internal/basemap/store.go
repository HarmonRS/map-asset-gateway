package basemap

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const (
	defaultScanSourceCode   = "local-basemaps"
	defaultManifestName     = "manifest.json"
	readPermission          = "read"
	defaultVectorSourceCode = "local-vectors"
)

const (
	// sqliteMaxOpenConns lets concurrent tile reads overlap instead of being
	// serialized behind a single connection.
	sqliteMaxOpenConns = 8
)

// sqliteDSN tunes each pooled connection. busy_timeout makes concurrent
// writers wait for the lock instead of failing immediately.
func sqliteDSN(path string) string {
	params := url.Values{}
	params.Add("_pragma", "busy_timeout(5000)")
	params.Add("_pragma", "foreign_keys(1)")
	params.Add("_pragma", "synchronous(NORMAL)")
	return "file:" + filepath.ToSlash(path) + "?" + params.Encode()
}

var (
	// ErrScanInProgress is returned when a scan is already running.
	ErrScanInProgress = errors.New("scan already in progress")
	// ErrScanSourceNotFound is returned when a scan source code is unknown.
	ErrScanSourceNotFound = errors.New("scan source not found")
	// ErrScanSourceDisabled is returned when a scan source is turned off.
	ErrScanSourceDisabled = errors.New("scan source disabled")
)

// scanSourceError keeps the operator-facing message stable while still matching
// errors.Is against a sentinel.
type scanSourceError struct {
	sentinel error
	code     string
	reason   string
}

func (e *scanSourceError) Error() string {
	return fmt.Sprintf("scan source %q %s", e.code, e.reason)
}

func (e *scanSourceError) Is(target error) bool {
	return target == e.sentinel
}

type StoreConfig struct {
	SQLitePath          string
	ScanRoot            string
	VectorScanRoot      string
	APIBaseURL          string
	TileBaseURL         string
	VectorBaseURL       string
	AutoScanOnStart     bool
	TokenDefaultTTL     time.Duration
	TileJobCommand      string
	TileJobVRTCommand   string
	TileJobLogDir       string
	HealthCheckInterval time.Duration
}

type Store struct {
	db         *sql.DB
	cfg        StoreConfig
	httpClient *http.Client

	// scanMu serializes scans so two runs cannot interleave their read-then-write
	// bookkeeping.
	scanMu sync.Mutex
	// pushWG tracks detached push callbacks so Close can drain them.
	pushWG sync.WaitGroup

	bgOnce   sync.Once
	bgCancel context.CancelFunc
	bgCtx    context.Context
	bgWG     sync.WaitGroup
}

type CreatedToken struct {
	Meta  ServiceToken `json:"meta"`
	Token string       `json:"token"`
}

type TileDescriptor struct {
	FilePath     string
	ContentType  string
	Basemap      Basemap
	Version      BasemapVersion
	Token        ServiceToken
	RelativePath string
}

func OpenStore(cfg StoreConfig) (*Store, error) {
	if strings.TrimSpace(cfg.SQLitePath) == "" {
		return nil, errors.New("sqlite path is required")
	}
	if err := os.MkdirAll(filepath.Dir(cfg.SQLitePath), 0o755); err != nil {
		return nil, fmt.Errorf("create sqlite dir: %w", err)
	}

	db, err := sql.Open("sqlite", sqliteDSN(cfg.SQLitePath))
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetConnMaxLifetime(30 * time.Minute)
	db.SetConnMaxIdleTime(5 * time.Minute)
	db.SetMaxOpenConns(sqliteMaxOpenConns)
	db.SetMaxIdleConns(sqliteMaxOpenConns)

	// WAL keeps tile reads from blocking on scans/pushes. It is persisted in the
	// database file, so enabling it once is enough; a filesystem that cannot
	// support WAL (some network shares) must not stop the service from starting.
	if _, err := db.Exec("PRAGMA journal_mode=WAL;"); err != nil {
		log.Printf("enable sqlite WAL skipped: %v", err)
	}

	store := &Store{
		db:  db,
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
	if err := store.bootstrap(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	if strings.TrimSpace(cfg.ScanRoot) != "" {
		if _, err := store.EnsureScanSource(context.Background(), CreateScanSourceInput{
			Code:         defaultScanSourceCode,
			Name:         "Local Basemaps",
			RootPath:     cfg.ScanRoot,
			ManifestName: defaultManifestName,
		}); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	if strings.TrimSpace(cfg.VectorScanRoot) != "" {
		if err := os.MkdirAll(cfg.VectorScanRoot, 0o755); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("create vector scan root: %w", err)
		}
	}
	return store, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	// Stop the periodic loops, then let detached push callbacks finish before
	// the pool goes away.
	if s.bgCancel != nil {
		s.bgCancel()
	}
	s.bgWG.Wait()
	s.pushWG.Wait()
	return s.db.Close()
}

func (s *Store) bootstrap(ctx context.Context) error {
	statements := []string{
		`PRAGMA foreign_keys = ON;`,
		`CREATE TABLE IF NOT EXISTS basemaps (
			id TEXT PRIMARY KEY,
			code TEXT NOT NULL UNIQUE,
			name TEXT NOT NULL,
			type TEXT NOT NULL,
			status TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS basemap_versions (
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
			health TEXT NOT NULL DEFAULT '',
			health_checked_at TEXT NOT NULL DEFAULT '',
			health_detail TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			UNIQUE(basemap_id, version),
			FOREIGN KEY (basemap_id) REFERENCES basemaps(id) ON DELETE CASCADE,
			FOREIGN KEY (scan_source_id) REFERENCES scan_sources(id) ON DELETE SET NULL
		);`,
		`CREATE TABLE IF NOT EXISTS scan_sources (
			id TEXT PRIMARY KEY,
			code TEXT NOT NULL UNIQUE,
			name TEXT NOT NULL,
			root_path TEXT NOT NULL,
			manifest_name TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			metadata_json TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS scan_runs (
			id TEXT PRIMARY KEY,
			scan_source_id TEXT NOT NULL,
			status TEXT NOT NULL,
			scanned_count INTEGER NOT NULL DEFAULT 0,
			added_count INTEGER NOT NULL DEFAULT 0,
			updated_count INTEGER NOT NULL DEFAULT 0,
			removed_count INTEGER NOT NULL DEFAULT 0,
			summary_json TEXT NOT NULL DEFAULT '',
			started_at TEXT NOT NULL,
			finished_at TEXT NOT NULL,
			FOREIGN KEY (scan_source_id) REFERENCES scan_sources(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS target_systems (
			id TEXT PRIMARY KEY,
			code TEXT NOT NULL UNIQUE,
			name TEXT NOT NULL,
			callback_url TEXT NOT NULL,
			callback_method TEXT NOT NULL,
			callback_headers_json TEXT NOT NULL DEFAULT '',
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS service_tokens (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			token_hash TEXT NOT NULL UNIQUE,
			token_prefix TEXT NOT NULL,
			scopes TEXT NOT NULL DEFAULT 'read',
			status TEXT NOT NULL,
			expires_at TEXT,
			last_used_at TEXT,
			created_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS token_grants (
			id TEXT PRIMARY KEY,
			token_id TEXT NOT NULL,
			basemap_id TEXT NOT NULL,
			basemap_version_id TEXT,
			permission TEXT NOT NULL,
			created_at TEXT NOT NULL,
			FOREIGN KEY (token_id) REFERENCES service_tokens(id) ON DELETE CASCADE,
			FOREIGN KEY (basemap_id) REFERENCES basemaps(id) ON DELETE CASCADE,
			FOREIGN KEY (basemap_version_id) REFERENCES basemap_versions(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS push_records (
			id TEXT PRIMARY KEY,
			target_system_id TEXT NOT NULL,
			basemap_version_id TEXT NOT NULL,
			status TEXT NOT NULL,
			request_json TEXT NOT NULL DEFAULT '',
			response_status INTEGER,
			response_body TEXT NOT NULL DEFAULT '',
			error_message TEXT NOT NULL DEFAULT '',
			attempt_count INTEGER NOT NULL DEFAULT 0,
			next_attempt_at TEXT,
			pushed_at TEXT NOT NULL,
			finished_at TEXT,
			FOREIGN KEY (target_system_id) REFERENCES target_systems(id) ON DELETE CASCADE,
			FOREIGN KEY (basemap_version_id) REFERENCES basemap_versions(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS vector_assets (
			id TEXT PRIMARY KEY,
			code TEXT NOT NULL UNIQUE,
			name TEXT NOT NULL,
			format TEXT NOT NULL,
			status TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			file_path TEXT NOT NULL,
			file_name TEXT NOT NULL,
			file_size INTEGER NOT NULL DEFAULT 0,
			checksum TEXT NOT NULL DEFAULT '',
			download_url TEXT NOT NULL,
			metadata_json TEXT NOT NULL DEFAULT '',
			health TEXT NOT NULL DEFAULT '',
			health_checked_at TEXT NOT NULL DEFAULT '',
			health_detail TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS vector_token_grants (
			id TEXT PRIMARY KEY,
			token_id TEXT NOT NULL,
			vector_asset_id TEXT NOT NULL,
			permission TEXT NOT NULL,
			created_at TEXT NOT NULL,
			FOREIGN KEY (token_id) REFERENCES service_tokens(id) ON DELETE CASCADE,
			FOREIGN KEY (vector_asset_id) REFERENCES vector_assets(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS vector_push_records (
			id TEXT PRIMARY KEY,
			target_system_id TEXT NOT NULL,
			vector_asset_id TEXT NOT NULL,
			status TEXT NOT NULL,
			request_json TEXT NOT NULL DEFAULT '',
			response_status INTEGER,
			response_body TEXT NOT NULL DEFAULT '',
			error_message TEXT NOT NULL DEFAULT '',
			attempt_count INTEGER NOT NULL DEFAULT 0,
			next_attempt_at TEXT,
			pushed_at TEXT NOT NULL,
			finished_at TEXT,
			FOREIGN KEY (target_system_id) REFERENCES target_systems(id) ON DELETE CASCADE,
			FOREIGN KEY (vector_asset_id) REFERENCES vector_assets(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS tile_jobs (
			id TEXT PRIMARY KEY,
			code TEXT NOT NULL UNIQUE,
			name TEXT NOT NULL,
			source_path TEXT NOT NULL,
			source_count INTEGER NOT NULL DEFAULT 1,
			sources_json TEXT NOT NULL DEFAULT '',
			target_code TEXT NOT NULL,
			output_root TEXT NOT NULL,
			zoom_min INTEGER NOT NULL DEFAULT 0,
			zoom_max INTEGER NOT NULL DEFAULT 0,
			tile_size INTEGER NOT NULL DEFAULT 256,
			tile_format TEXT NOT NULL DEFAULT 'webp',
			status TEXT NOT NULL,
			progress TEXT NOT NULL DEFAULT '',
			message TEXT NOT NULL DEFAULT '',
			log_path TEXT NOT NULL DEFAULT '',
			callback_url TEXT NOT NULL DEFAULT '',
			callback_status TEXT NOT NULL DEFAULT '',
			callback_attempts INTEGER NOT NULL DEFAULT 0,
			callback_next_attempt_at TEXT,
			callback_message TEXT NOT NULL DEFAULT '',
			requested_by TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			started_at TEXT,
			finished_at TEXT
		);`,
		`CREATE INDEX IF NOT EXISTS idx_tile_jobs_status ON tile_jobs(status, created_at);`,
		`CREATE INDEX IF NOT EXISTS idx_tile_jobs_target ON tile_jobs(target_code);`,
		`CREATE TABLE IF NOT EXISTS layer_sources (
			id TEXT PRIMARY KEY,
			target_code TEXT NOT NULL,
			source_path TEXT NOT NULL,
			added_at TEXT NOT NULL,
			UNIQUE(target_code, source_path)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_layer_sources_target ON layer_sources(target_code);`,
		`CREATE INDEX IF NOT EXISTS idx_versions_basemap ON basemap_versions(basemap_id);`,
		`CREATE INDEX IF NOT EXISTS idx_versions_source ON basemap_versions(scan_source_id);`,
		`CREATE INDEX IF NOT EXISTS idx_grants_token ON token_grants(token_id);`,
		`CREATE INDEX IF NOT EXISTS idx_push_target ON push_records(target_system_id);`,
		`CREATE INDEX IF NOT EXISTS idx_vector_file_name ON vector_assets(file_name COLLATE NOCASE);`,
		`CREATE INDEX IF NOT EXISTS idx_vector_grants_token ON vector_token_grants(token_id);`,
		`CREATE INDEX IF NOT EXISTS idx_vector_push_target ON vector_push_records(target_system_id);`,
	}

	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("bootstrap sqlite: %w", err)
		}
	}

	// CREATE TABLE IF NOT EXISTS never evolves an existing database, so columns
	// added after the first release are patched in here.
	migrations := []struct {
		table      string
		column     string
		definition string
	}{
		{"basemap_versions", "health", "TEXT NOT NULL DEFAULT ''"},
		{"basemap_versions", "health_checked_at", "TEXT NOT NULL DEFAULT ''"},
		{"basemap_versions", "health_detail", "TEXT NOT NULL DEFAULT ''"},
		{"vector_assets", "health", "TEXT NOT NULL DEFAULT ''"},
		{"vector_assets", "health_checked_at", "TEXT NOT NULL DEFAULT ''"},
		{"vector_assets", "health_detail", "TEXT NOT NULL DEFAULT ''"},
		{"push_records", "attempt_count", "INTEGER NOT NULL DEFAULT 0"},
		{"push_records", "next_attempt_at", "TEXT"},
		{"vector_push_records", "attempt_count", "INTEGER NOT NULL DEFAULT 0"},
		{"vector_push_records", "next_attempt_at", "TEXT"},
		{"service_tokens", "scopes", "TEXT NOT NULL DEFAULT 'read'"},
		{"tile_jobs", "source_count", "INTEGER NOT NULL DEFAULT 1"},
		{"tile_jobs", "sources_json", "TEXT NOT NULL DEFAULT ''"},
		{"tile_jobs", "callback_url", "TEXT NOT NULL DEFAULT ''"},
		{"tile_jobs", "callback_status", "TEXT NOT NULL DEFAULT ''"},
		{"tile_jobs", "callback_attempts", "INTEGER NOT NULL DEFAULT 0"},
		{"tile_jobs", "callback_next_attempt_at", "TEXT"},
		{"tile_jobs", "callback_message", "TEXT NOT NULL DEFAULT ''"},
	}
	for _, migration := range migrations {
		if err := s.ensureColumn(ctx, migration.table, migration.column, migration.definition); err != nil {
			return fmt.Errorf("bootstrap migrate %s.%s: %w", migration.table, migration.column, err)
		}
	}
	// These indexes reference migrated columns, so they run after the patch.
	for _, statement := range []string{
		`CREATE INDEX IF NOT EXISTS idx_push_next_attempt ON push_records(next_attempt_at);`,
		`CREATE INDEX IF NOT EXISTS idx_vector_push_next_attempt ON vector_push_records(next_attempt_at);`,
	} {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("bootstrap index: %w", err)
		}
	}
	return nil
}

// ensureColumn adds a column when an existing table is missing it. PRAGMA
// table_info is the portable way to test SQLite column presence.
func (s *Store) ensureColumn(ctx context.Context, table, column, definition string) error {
	rows, err := s.db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "ALTER TABLE "+table+" ADD COLUMN "+column+" "+definition)
	return err
}

func (s *Store) AutoScanEnabled() bool {
	return s.cfg.AutoScanOnStart
}

func (s *Store) ScanRoot() string {
	return s.cfg.ScanRoot
}

func (s *Store) VectorScanRoot() string {
	return s.cfg.VectorScanRoot
}

func (s *Store) generateTokenSecret() (string, string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", "", err
	}

	encoder := base32.StdEncoding.WithPadding(base32.NoPadding)
	secret := strings.ToLower(encoder.EncodeToString(raw[:]))
	token := "bmp_" + secret
	sum := sha256.Sum256([]byte(token))
	return token, hex.EncodeToString(sum[:]), nil
}

func normalizeBasemapCode(value string) string {
	return normalizeCode(value)
}

func nowUTC() time.Time {
	return time.Now().UTC().Round(time.Second)
}

func toRFC3339(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

func nullableTime(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return toRFC3339(*t)
}

func scanNullString(value string) sql.NullString {
	if strings.TrimSpace(value) == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: value, Valid: true}
}

func readMaybeJSON[T any](value string, target *T) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return json.Unmarshal([]byte(value), target)
}

func writeJSON(value any) string {
	if value == nil {
		return ""
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func sortedKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func trimURL(value string) string {
	return strings.TrimRight(strings.TrimSpace(value), "/")
}

func limitedReadAll(body io.Reader, limit int64) string {
	content, err := io.ReadAll(io.LimitReader(body, limit))
	if err != nil {
		return ""
	}
	return string(content)
}
