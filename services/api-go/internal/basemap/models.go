package basemap

import "time"

type SystemInfo struct {
	ServiceName    string    `json:"service_name"`
	ServiceVersion string    `json:"service_version"`
	Commit         string    `json:"commit"`
	Env            string    `json:"env"`
	APIBaseURL     string    `json:"api_base_url"`
	TileBaseURL    string    `json:"tile_base_url"`
	VectorBaseURL  string    `json:"vector_base_url"`
	SQLitePath     string    `json:"sqlite_path"`
	ScanRoot       string    `json:"scan_root"`
	VectorScanRoot string    `json:"vector_scan_root"`
	TileJobCommand string    `json:"tile_job_command,omitempty"`
	StartedAt      time.Time `json:"started_at"`
}

type Basemap struct {
	ID          string           `json:"id"`
	Code        string           `json:"code"`
	Name        string           `json:"name"`
	Type        string           `json:"type"`
	Status      string           `json:"status"`
	Description string           `json:"description,omitempty"`
	Default     *BasemapVersion  `json:"default_version,omitempty"`
	Versions    []BasemapVersion `json:"versions,omitempty"`
	CreatedAt   time.Time        `json:"created_at"`
	UpdatedAt   time.Time        `json:"updated_at"`
}

type BasemapVersion struct {
	ID           string         `json:"id"`
	BasemapID    string         `json:"basemap_id"`
	BasemapCode  string         `json:"basemap_code"`
	Version      string         `json:"version"`
	Status       string         `json:"status"`
	IsDefault    bool           `json:"is_default"`
	ManifestPath string         `json:"manifest_path"`
	TileRootPath string         `json:"tile_root_path"`
	URLTemplate  string         `json:"url_template"`
	TileFormat   string         `json:"tile_format"`
	TileScheme   string         `json:"tile_scheme"`
	MinZoom      int            `json:"min_zoom"`
	MaxZoom      int            `json:"max_zoom"`
	BBox         []float64      `json:"bbox,omitempty"`
	Attribution  string         `json:"attribution,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

type ScanSource struct {
	ID           string         `json:"id"`
	Code         string         `json:"code"`
	Name         string         `json:"name"`
	RootPath     string         `json:"root_path"`
	ManifestName string         `json:"manifest_name"`
	Enabled      bool           `json:"enabled"`
	Metadata     map[string]any `json:"metadata,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

type ScanRun struct {
	ID           string         `json:"id"`
	ScanSourceID string         `json:"scan_source_id"`
	SourceCode   string         `json:"source_code"`
	Status       string         `json:"status"`
	ScannedCount int            `json:"scanned_count"`
	AddedCount   int            `json:"added_count"`
	UpdatedCount int            `json:"updated_count"`
	RemovedCount int            `json:"removed_count"`
	Summary      map[string]any `json:"summary,omitempty"`
	StartedAt    time.Time      `json:"started_at"`
	FinishedAt   time.Time      `json:"finished_at"`
}

type TargetSystem struct {
	ID              string            `json:"id"`
	Code            string            `json:"code"`
	Name            string            `json:"name"`
	CallbackURL     string            `json:"callback_url"`
	CallbackMethod  string            `json:"callback_method"`
	CallbackHeaders map[string]string `json:"callback_headers,omitempty"`
	Enabled         bool              `json:"enabled"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

type ServiceToken struct {
	ID           string             `json:"id"`
	Name         string             `json:"name"`
	TokenPrefix  string             `json:"token_prefix"`
	Scopes       []string           `json:"scopes"`
	Status       string             `json:"status"`
	ExpiresAt    *time.Time         `json:"expires_at,omitempty"`
	LastUsedAt   *time.Time         `json:"last_used_at,omitempty"`
	CreatedAt    time.Time          `json:"created_at"`
	Grants       []TokenGrant       `json:"grants,omitempty"`
	VectorGrants []VectorTokenGrant `json:"vector_grants,omitempty"`
}

type TokenGrant struct {
	ID               string    `json:"id"`
	TokenID          string    `json:"token_id"`
	BasemapID        string    `json:"basemap_id"`
	BasemapCode      string    `json:"basemap_code"`
	BasemapVersionID *string   `json:"basemap_version_id,omitempty"`
	BasemapVersion   *string   `json:"basemap_version,omitempty"`
	Permission       string    `json:"permission"`
	CreatedAt        time.Time `json:"created_at"`
}

type PushRecord struct {
	ID               string         `json:"id"`
	TargetSystemID   string         `json:"target_system_id"`
	TargetSystemCode string         `json:"target_system_code"`
	BasemapVersionID string         `json:"basemap_version_id"`
	BasemapCode      string         `json:"basemap_code"`
	BasemapVersion   string         `json:"basemap_version"`
	Status           string         `json:"status"`
	Request          map[string]any `json:"request,omitempty"`
	ResponseStatus   *int           `json:"response_status,omitempty"`
	ResponseBody     string         `json:"response_body,omitempty"`
	ErrorMessage     string         `json:"error_message,omitempty"`
	AttemptCount     int            `json:"attempt_count"`
	NextAttemptAt    *time.Time     `json:"next_attempt_at,omitempty"`
	PushedAt         time.Time      `json:"pushed_at"`
	FinishedAt       *time.Time     `json:"finished_at,omitempty"`
}

type VectorAsset struct {
	ID          string         `json:"id"`
	Code        string         `json:"code"`
	Name        string         `json:"name"`
	Format      string         `json:"format"`
	Status      string         `json:"status"`
	Description string         `json:"description,omitempty"`
	FilePath    string         `json:"file_path"`
	FileName    string         `json:"file_name"`
	FileSize    int64          `json:"file_size"`
	Checksum    string         `json:"checksum"`
	DownloadURL string         `json:"download_url"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

type VectorTokenGrant struct {
	ID         string    `json:"id"`
	TokenID    string    `json:"token_id"`
	VectorID   string    `json:"vector_id"`
	VectorCode string    `json:"vector_code"`
	Permission string    `json:"permission"`
	CreatedAt  time.Time `json:"created_at"`
}

type VectorPushRecord struct {
	ID               string         `json:"id"`
	TargetSystemID   string         `json:"target_system_id"`
	TargetSystemCode string         `json:"target_system_code"`
	VectorAssetID    string         `json:"vector_asset_id"`
	VectorCode       string         `json:"vector_code"`
	Status           string         `json:"status"`
	Request          map[string]any `json:"request,omitempty"`
	ResponseStatus   *int           `json:"response_status,omitempty"`
	ResponseBody     string         `json:"response_body,omitempty"`
	ErrorMessage     string         `json:"error_message,omitempty"`
	AttemptCount     int            `json:"attempt_count"`
	NextAttemptAt    *time.Time     `json:"next_attempt_at,omitempty"`
	PushedAt         time.Time      `json:"pushed_at"`
	FinishedAt       *time.Time     `json:"finished_at,omitempty"`
}

type Manifest struct {
	Code        string         `json:"code"`
	Name        string         `json:"name"`
	Type        string         `json:"type"`
	Description string         `json:"description"`
	Version     string         `json:"version"`
	Status      string         `json:"status"`
	TileFormat  string         `json:"tile_format"`
	TileScheme  string         `json:"tile_scheme"`
	MinZoom     int            `json:"min_zoom"`
	MaxZoom     int            `json:"max_zoom"`
	BBox        []float64      `json:"bbox"`
	Attribution string         `json:"attribution"`
	RootPath    string         `json:"root_path"`
	IsDefault   bool           `json:"is_default"`
	Metadata    map[string]any `json:"metadata"`
}

type CreateScanSourceInput struct {
	Code         string `json:"code"`
	Name         string `json:"name"`
	RootPath     string `json:"root_path"`
	ManifestName string `json:"manifest_name"`
}

type CreateTargetSystemInput struct {
	Code            string            `json:"code"`
	Name            string            `json:"name"`
	CallbackURL     string            `json:"callback_url"`
	CallbackMethod  string            `json:"callback_method"`
	CallbackHeaders map[string]string `json:"callback_headers"`
}

type CreateTokenInput struct {
	Name         string
	BasemapCodes []string
	VectorCodes  []string
	Scopes       []string
	// NoExpiry creates a token that never expires, overriding the default TTL.
	NoExpiry  bool
	ExpiresAt *time.Time
}

type TokenAuth struct {
	Token ServiceToken
	// permission -> basemap_code -> version scope
	Permissions       map[string]map[string]VersionScope
	VectorPermissions map[string]map[string]struct{}
	// Scopes are coarse capabilities beyond resource grants, e.g. "slice".
	Scopes map[string]struct{}
}

type VersionScope struct {
	AllVersions bool
	Versions    map[string]struct{}
}

// ResourceHealth is the availability verdict recorded by the health inspector.
// It is kept separate from Status: Status is a catalog fact, Health is whether
// the bytes are still on disk right now.
type ResourceHealth struct {
	Health    string    `json:"health"`
	Detail    string    `json:"detail,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
}

// LayerInventory is one layer and the imagery it is built from, ordered for
// deterministic rendering in the console.
type LayerInventory struct {
	TargetCode string        `json:"target_code"`
	Sources    []LayerSource `json:"sources"`
	// ZoomMin/ZoomMax/TileFormat mirror the layer's current basemap version so
	// a rebuild can reuse the range it is already published at.
	ZoomMin    int    `json:"zoom_min"`
	ZoomMax    int    `json:"zoom_max"`
	TileFormat string `json:"tile_format"`
}

type DashboardData struct {
	SystemInfo        SystemInfo
	Basemaps          []Basemap
	VectorAssets      []VectorAsset
	ScanSources       []ScanSource
	ScanRuns          []ScanRun
	TargetSystems     []TargetSystem
	Tokens            []ServiceToken
	PushRecords       []PushRecord
	VectorPushRecords []VectorPushRecord
	Flash             string
	CreatedToken      string
	VersionHealth     map[string]ResourceHealth
	VectorHealth      map[string]ResourceHealth
	TileJobs          []TileJob
	Layers            []LayerInventory
}
