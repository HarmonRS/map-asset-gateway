package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Host                string
	Port                int
	APIBaseURL          string
	TilePublicBaseURL   string
	VectorPublicBaseURL string
	SQLitePath          string
	BasemapScanRoot     string
	VectorScanRoot      string
	AutoScanOnStart     bool
	Env                 string
	ServiceName         string
	ServiceVersion      string
	Commit              string

	AdminToken         string
	AdminUser          string
	AdminPassword      string
	CORSAllowedOrigins []string
	TokenDefaultTTL    time.Duration

	HealthCheckInterval time.Duration
	TileJobCommand      string
	TileJobVRTCommand   string
	TileJobLogDir       string
}

func Load() (Config, error) {
	if err := LoadEnvDefaults(); err != nil {
		return Config{}, err
	}

	port, err := intEnv("API_PORT", 8910)
	if err != nil {
		return Config{}, fmt.Errorf("parse API_PORT: %w", err)
	}
	autoScanOnStart, err := boolEnv("AUTO_SCAN_ON_START", true)
	if err != nil {
		return Config{}, fmt.Errorf("parse AUTO_SCAN_ON_START: %w", err)
	}
	tokenDefaultTTL, err := durationEnv("TOKEN_DEFAULT_TTL", 720*time.Hour)
	if err != nil {
		return Config{}, fmt.Errorf("parse TOKEN_DEFAULT_TTL: %w", err)
	}
	healthCheckInterval, err := durationEnv("HEALTH_CHECK_INTERVAL", 5*time.Minute)
	if err != nil {
		return Config{}, fmt.Errorf("parse HEALTH_CHECK_INTERVAL: %w", err)
	}

	cfg := Config{
		Host:                stringEnv("API_HOST", "127.0.0.1"),
		Port:                port,
		APIBaseURL:          stringEnv("API_BASE_URL", ""),
		TilePublicBaseURL:   stringEnv("TILE_PUBLIC_BASE_URL", ""),
		VectorPublicBaseURL: stringEnv("VECTOR_PUBLIC_BASE_URL", ""),
		SQLitePath:          stringEnv("SQLITE_PATH", filepath.Clean(filepath.Join("data", "basemap-pusher.db"))),
		BasemapScanRoot:     stringEnv("BASEMAP_SCAN_ROOT", filepath.Clean(filepath.Join("data", "tiles"))),
		VectorScanRoot:      stringEnv("VECTOR_SCAN_ROOT", filepath.Clean(filepath.Join("data", "geojson"))),
		AutoScanOnStart:     autoScanOnStart,
		Env:                 stringEnv("APP_ENV", "dev"),
		ServiceName:         stringEnv("SERVICE_NAME", "basemap-pusher"),
		ServiceVersion:      stringEnv("SERVICE_VERSION", "0.2.0"),
		Commit:              stringEnv("SERVICE_COMMIT", "local"),
		AdminToken:          strings.TrimSpace(stringEnv("ADMIN_TOKEN", "")),
		AdminUser:           strings.TrimSpace(stringEnv("ADMIN_USER", "")),
		AdminPassword:       stringEnv("ADMIN_PASSWORD", ""),
		CORSAllowedOrigins:  listEnv("CORS_ALLOWED_ORIGINS"),
		TokenDefaultTTL:     tokenDefaultTTL,
		HealthCheckInterval: healthCheckInterval,
		TileJobCommand:      stringEnv("TILE_JOB_COMMAND", ""),
		TileJobVRTCommand:   stringEnv("TILE_JOB_VRT_COMMAND", ""),
		TileJobLogDir:       stringEnv("TILE_JOB_LOG_DIR", ""),
	}
	if cfg.APIBaseURL == "" {
		cfg.APIBaseURL = deriveBaseURL(cfg.Host, cfg.Port)
	}
	if cfg.TilePublicBaseURL == "" {
		cfg.TilePublicBaseURL = cfg.APIBaseURL
	}
	if cfg.VectorPublicBaseURL == "" {
		cfg.VectorPublicBaseURL = cfg.APIBaseURL
	}
	return cfg, nil
}

func (c Config) ListenAddr() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

// HasAdminCredentials reports whether the admin console has any credential
// configured. Without one, the admin surface is only safe on loopback.
func (c Config) HasAdminCredentials() bool {
	return c.AdminToken != "" || (c.AdminUser != "" && c.AdminPassword != "")
}

// BindsToNonLoopback reports whether the listen address is reachable from
// outside the host, which makes credentials mandatory.
func (c Config) BindsToNonLoopback() bool {
	return !isLoopbackHost(c.Host)
}

func deriveBaseURL(host string, port int) string {
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return fmt.Sprintf("http://%s:%d", host, port)
}

func isLoopbackHost(host string) bool {
	host = strings.TrimSpace(host)
	if host == "" || strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

func stringEnv(key, fallback string) string {
	value, ok := os.LookupEnv(key)
	if ok && value != "" {
		return value
	}
	return fallback
}

func intEnv(key string, fallback int) (int, error) {
	value, ok := os.LookupEnv(key)
	if ok && value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return 0, err
		}
		return parsed, nil
	}
	return fallback, nil
}

func boolEnv(key string, fallback bool) (bool, error) {
	value, ok := os.LookupEnv(key)
	if ok && value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return false, err
		}
		return parsed, nil
	}
	return fallback, nil
}

func durationEnv(key string, fallback time.Duration) (time.Duration, error) {
	value, ok := os.LookupEnv(key)
	if ok && strings.TrimSpace(value) != "" {
		parsed, err := time.ParseDuration(strings.TrimSpace(value))
		if err != nil {
			return 0, err
		}
		return parsed, nil
	}
	return fallback, nil
}

func listEnv(key string) []string {
	raw := strings.FieldsFunc(stringEnv(key, ""), func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	})
	values := make([]string, 0, len(raw))
	for _, item := range raw {
		item = strings.TrimSpace(item)
		if item != "" {
			values = append(values, item)
		}
	}
	return values
}
