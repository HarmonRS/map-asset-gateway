package basemapapi

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"tile-server/api-go/internal/basemap"
)

// AdminAuth protects the admin console and admin API. When neither credential
// is configured the admin surface is only safe on loopback, which main enforces.
type AdminAuth struct {
	Token    string
	User     string
	Password string
	// AllowRemote permits admin requests from other hosts. Off by default: the
	// service can then serve tiles to the LAN while the console stays local.
	AllowRemote bool
}

func (a AdminAuth) enabled() bool {
	return strings.TrimSpace(a.Token) != "" || (strings.TrimSpace(a.User) != "" && a.Password != "")
}

// Options carries wiring that must not be exposed through the public system
// info payload (credentials, CORS policy).
type Options struct {
	Admin              AdminAuth
	CORSAllowedOrigins []string
}

// metrics holds cheap in-process counters for the /metrics endpoint.
type metrics struct {
	requests     atomic.Int64
	status2xx    atomic.Int64
	status4xx    atomic.Int64
	status5xx    atomic.Int64
	tiles        atomic.Int64
	tileMisses   atomic.Int64
	authFailures atomic.Int64
}
type Server struct {
	store       *basemap.Store
	system      basemap.SystemInfo
	startedAt   time.Time
	template    *template.Template
	mux         *http.ServeMux
	admin       AdminAuth
	corsOrigins []string
	metrics     *metrics
}

func New(store *basemap.Store, system basemap.SystemInfo, options Options) (*Server, error) {
	tmpl, err := template.New("admin").Funcs(template.FuncMap{
		"fmtTime": func(value time.Time) string {
			if value.IsZero() {
				return "-"
			}
			return value.Local().Format("2006-01-02 15:04:05")
		},
		"fmtTimePtr": func(value *time.Time) string {
			if value == nil || value.IsZero() {
				return "-"
			}
			return value.Local().Format("2006-01-02 15:04:05")
		},
		"defaultVersion": func(value *basemap.BasemapVersion) string {
			if value == nil {
				return "-"
			}
			return value.Version
		},
		"tokenGrants": func(grants []basemap.TokenGrant) string {
			if len(grants) == 0 {
				return "-"
			}
			parts := make([]string, 0, len(grants))
			for _, grant := range grants {
				if grant.BasemapVersion != nil {
					parts = append(parts, grant.BasemapCode+":"+*grant.BasemapVersion)
				} else {
					parts = append(parts, grant.BasemapCode+":*")
				}
			}
			return strings.Join(parts, ", ")
		},
		"hasScope": func(scopes []string, want string) bool {
			for _, scope := range scopes {
				if scope == want {
					return true
				}
			}
			return false
		},
		"vectorTokenGrants": func(grants []basemap.VectorTokenGrant) string {
			if len(grants) == 0 {
				return "-"
			}
			parts := make([]string, 0, len(grants))
			for _, grant := range grants {
				parts = append(parts, grant.VectorCode)
			}
			return strings.Join(parts, ", ")
		},
		"versionLabel": func(item basemap.BasemapVersion) string {
			if item.IsDefault {
				return item.Version + "（默认）"
			}
			return item.Version
		},
		"vectorPath": func(item basemap.VectorAsset) string {
			if value, ok := item.Metadata["relative_path"].(string); ok && strings.TrimSpace(value) != "" {
				return value
			}
			return item.FileName
		},
		"statusText": statusText,
		"statusTone": statusTone,
		"enabledText": func(enabled bool) string {
			if enabled {
				return "启用"
			}
			return "停用"
		},
		"enabledTone": func(enabled bool) string {
			if enabled {
				return "success"
			}
			return "danger"
		},
		"formatBytes": formatBytes,
		"flashClass": func(value string) string {
			if strings.HasPrefix(strings.TrimSpace(value), "操作失败：") {
				return "danger"
			}
			return ""
		},
	}).Parse(adminTemplate)
	if err != nil {
		return nil, err
	}

	server := &Server{
		store:       store,
		system:      system,
		startedAt:   time.Now().UTC(),
		template:    tmpl,
		mux:         http.NewServeMux(),
		admin:       options.Admin,
		corsOrigins: normalizeOrigins(options.CORSAllowedOrigins),
		metrics:     &metrics{},
	}
	server.routes()
	return server, nil
}

func (s *Server) Handler() http.Handler {
	return withAccessLog(withCORS(s.mux, s.corsOrigins), s.metrics)
}

func normalizeOrigins(values []string) []string {
	origins := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			origins = append(origins, value)
		}
	}
	return origins
}

// withCORS only reflects origins that were explicitly allowed. An empty
// allow-list means no cross-origin access at all, rather than echoing any site.
func withCORS(next http.Handler, allowed []string) http.Handler {
	allowAll := false
	allowedSet := map[string]struct{}{}
	for _, origin := range allowed {
		if origin == "*" {
			allowAll = true
		}
		allowedSet[strings.ToLower(origin)] = struct{}{}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin != "" {
			_, ok := allowedSet[strings.ToLower(origin)]
			if ok || allowAll {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Access-Token")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
			}
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(p)
	r.bytes += n
	return n, err
}

// withAccessLog records one line per request and feeds /metrics. Credentials
// carried in the query string are redacted before logging.
func withAccessLog(next http.Handler, stats *metrics) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startedAt := time.Now()
		recorder := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(recorder, r)
		if recorder.status == 0 {
			recorder.status = http.StatusOK
		}

		stats.requests.Add(1)
		switch {
		case recorder.status >= 500:
			stats.status5xx.Add(1)
		case recorder.status >= 400:
			stats.status4xx.Add(1)
		case recorder.status >= 200:
			stats.status2xx.Add(1)
		}
		if strings.HasPrefix(r.URL.Path, "/tiles/") {
			stats.tiles.Add(1)
			if recorder.status == http.StatusNotFound {
				stats.tileMisses.Add(1)
			}
		}
		if recorder.status == http.StatusUnauthorized {
			stats.authFailures.Add(1)
		}

		if r.URL.Path == "/health" || r.URL.Path == "/ready" || r.URL.Path == "/metrics" {
			return
		}
		log.Printf("%s %s %d %dB %s", r.Method, redactRequestURI(r), recorder.status, recorder.bytes, time.Since(startedAt).Round(time.Millisecond))
	})
}

func redactRequestURI(r *http.Request) string {
	if r.URL.RawQuery == "" {
		return r.URL.Path
	}
	redacted := url.Values{}
	for key, values := range r.URL.Query() {
		switch strings.ToLower(key) {
		case "token", "access_token":
			redacted.Set(key, "***")
		default:
			for _, value := range values {
				redacted.Add(key, value)
			}
		}
	}
	if encoded := redacted.Encode(); encoded != "" {
		return r.URL.Path + "?" + encoded
	}
	return r.URL.Path
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/", http.StatusFound)
	})
	s.mux.HandleFunc("GET /health", s.handleHealth)
	s.mux.HandleFunc("GET /ready", s.handleReady)
	s.mux.HandleFunc("GET /metrics", s.handleMetrics)
	s.mux.HandleFunc("GET /api/v1/system", s.handleSystem)

	// Creating a token here grants read access to every basemap, so the whole
	// admin surface sits behind the admin credentials.
	s.mux.HandleFunc("GET /api/v1/admin/dashboard", s.requireAdmin(s.handleDashboardJSON))
	s.mux.HandleFunc("GET /api/v1/admin/basemaps", s.requireAdmin(s.handleBasemapsJSON))
	s.mux.HandleFunc("GET /api/v1/admin/vectors", s.requireAdmin(s.handleVectorsJSON))
	s.mux.HandleFunc("GET /api/v1/admin/scan-sources", s.requireAdmin(s.handleScanSourcesJSON))
	s.mux.HandleFunc("POST /api/v1/admin/scan-sources", s.requireAdmin(s.handleCreateScanSourceJSON))
	s.mux.HandleFunc("POST /api/v1/admin/scan-sources/{code}/run", s.requireAdmin(s.handleRunScanJSON))
	s.mux.HandleFunc("POST /api/v1/admin/vectors/rescan", s.requireAdmin(s.handleRunVectorScanJSON))
	s.mux.HandleFunc("POST /api/v1/admin/basemaps/{code}/default-version", s.requireAdmin(s.handleSetDefaultVersionJSON))
	s.mux.HandleFunc("GET /api/v1/admin/targets", s.requireAdmin(s.handleTargetsJSON))
	s.mux.HandleFunc("POST /api/v1/admin/targets", s.requireAdmin(s.handleCreateTargetJSON))
	s.mux.HandleFunc("GET /api/v1/admin/tokens", s.requireAdmin(s.handleTokensJSON))
	s.mux.HandleFunc("POST /api/v1/admin/tokens", s.requireAdmin(s.handleCreateTokenJSON))
	s.mux.HandleFunc("POST /api/v1/admin/tokens/{id}/disable", s.requireAdmin(s.handleDisableTokenJSON))
	s.mux.HandleFunc("DELETE /api/v1/admin/tokens/{id}", s.requireAdmin(s.handleDeleteTokenJSON))
	s.mux.HandleFunc("GET /api/v1/admin/push-records", s.requireAdmin(s.handlePushRecordsJSON))
	s.mux.HandleFunc("POST /api/v1/admin/pushes", s.requireAdmin(s.handlePushJSON))
	s.mux.HandleFunc("GET /api/v1/admin/vector-push-records", s.requireAdmin(s.handleVectorPushRecordsJSON))
	s.mux.HandleFunc("POST /api/v1/admin/vector-pushes", s.requireAdmin(s.handleVectorPushJSON))
	s.mux.HandleFunc("POST /api/v1/admin/health/check", s.requireAdmin(s.handleCheckHealthJSON))
	s.mux.HandleFunc("GET /api/v1/admin/tile-jobs", s.requireAdmin(s.handleTileJobsJSON))
	s.mux.HandleFunc("POST /api/v1/admin/tile-jobs", s.requireAdmin(s.handleCreateTileJobJSON))
	s.mux.HandleFunc("POST /api/v1/admin/push-records/{id}/retry", s.requireAdmin(s.handleRetryPushJSON))
	s.mux.HandleFunc("POST /api/v1/admin/vector-push-records/{id}/retry", s.requireAdmin(s.handleRetryVectorPushJSON))

	// Slicing for external callers: a token with the "slice" scope, not admin.
	s.mux.HandleFunc("POST /api/v1/slice/jobs", s.handleSliceJobCreate)
	s.mux.HandleFunc("GET /api/v1/slice/jobs/{id}", s.handleSliceJobGet)
	s.mux.HandleFunc("GET /api/v1/slice/layers/{code}/sources", s.handleSliceLayerSources)

	s.mux.HandleFunc("GET /api/v1/catalog/basemaps", s.handleCatalogList)
	s.mux.HandleFunc("GET /api/v1/catalog/basemaps/{code}", s.handleCatalogGet)
	s.mux.HandleFunc("GET /api/v1/catalog/vectors", s.handleVectorCatalogList)
	s.mux.HandleFunc("GET /api/v1/catalog/vectors/{code}", s.handleVectorCatalogGet)
	s.mux.HandleFunc("GET /tiles/{code}/{version}/{tilePath...}", s.handleTile)
	s.mux.HandleFunc("GET /geojson/{assetPath...}", s.handleGeoJSONByPath)
	s.mux.HandleFunc("GET /shp/{assetPath...}", s.handleShapeByPath)
	s.mux.HandleFunc("GET /vectors/{code}/download", s.handleVectorDownload)

	s.mux.HandleFunc("GET /admin/", s.requireAdmin(s.handleAdminPage))
	s.mux.HandleFunc("POST /admin/scan-sources", s.requireAdmin(s.handleCreateScanSourceForm))
	s.mux.HandleFunc("POST /admin/scan-sources/{code}/run", s.requireAdmin(s.handleRunScanForm))
	s.mux.HandleFunc("POST /admin/vectors/rescan", s.requireAdmin(s.handleRunVectorScanForm))
	s.mux.HandleFunc("POST /admin/basemaps/{code}/default", s.requireAdmin(s.handleSetDefaultVersionForm))
	s.mux.HandleFunc("POST /admin/targets", s.requireAdmin(s.handleCreateTargetForm))
	s.mux.HandleFunc("POST /admin/tokens", s.requireAdmin(s.handleCreateTokenForm))
	s.mux.HandleFunc("POST /admin/tokens/{id}/disable", s.requireAdmin(s.handleDisableTokenForm))
	s.mux.HandleFunc("POST /admin/tokens/{id}/delete", s.requireAdmin(s.handleDeleteTokenForm))
	s.mux.HandleFunc("POST /admin/pushes", s.requireAdmin(s.handlePushForm))
	s.mux.HandleFunc("POST /admin/vector-pushes", s.requireAdmin(s.handleVectorPushForm))
	s.mux.HandleFunc("POST /admin/health/check", s.requireAdmin(s.handleCheckHealthForm))
	s.mux.HandleFunc("POST /admin/tile-jobs", s.requireAdmin(s.handleCreateTileJobForm))
	s.mux.HandleFunc("POST /admin/tile-jobs/rebuild", s.requireAdmin(s.handleRebuildLayerForm))
	s.mux.HandleFunc("POST /admin/push-records/{id}/retry", s.requireAdmin(s.handleRetryPushForm))
	s.mux.HandleFunc("POST /admin/vector-push-records/{id}/retry", s.requireAdmin(s.handleRetryVectorPushForm))
}

// requireAdmin guards the admin surface. With no credentials configured the
// service is only reachable on loopback, which main enforces at startup.
func (s *Server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.admin.AllowRemote && !isLoopbackRequest(r) {
			writeError(w, http.StatusForbidden, errors.New("admin endpoints are loopback-only; set ADMIN_ALLOW_REMOTE=true to accept remote callers"))
			return
		}
		if !s.admin.enabled() {
			next(w, r)
			return
		}
		if s.adminAllows(r) {
			next(w, r)
			return
		}
		w.Header().Set("WWW-Authenticate", `Basic realm="basemap-pusher admin"`)
		http.Error(w, "admin credentials required", http.StatusUnauthorized)
	}
}

// isLoopbackRequest reports whether the request reached us over a loopback
// address. RemoteAddr is the only trustworthy source here; X-Forwarded-For is
// attacker-controlled unless a trusted proxy is in front.
func isLoopbackRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		host = strings.TrimSpace(r.RemoteAddr)
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}
func (s *Server) adminAllows(r *http.Request) bool {
	if token := strings.TrimSpace(s.admin.Token); token != "" {
		header := strings.TrimSpace(r.Header.Get("Authorization"))
		if len(header) > 7 && strings.EqualFold(header[:7], "bearer ") {
			if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(header[7:])), []byte(token)) == 1 {
				return true
			}
		}
	}
	user := strings.TrimSpace(s.admin.User)
	if user == "" || s.admin.Password == "" {
		return false
	}
	givenUser, givenPassword, ok := r.BasicAuth()
	if !ok {
		return false
	}
	userOK := subtle.ConstantTimeCompare([]byte(givenUser), []byte(user)) == 1
	passwordOK := subtle.ConstantTimeCompare([]byte(givenPassword), []byte(s.admin.Password)) == 1
	return userOK && passwordOK
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	fmt.Fprintf(w, "# TYPE basemap_pusher_requests_total counter\n")
	fmt.Fprintf(w, "basemap_pusher_requests_total %d\n", s.metrics.requests.Load())
	fmt.Fprintf(w, "# TYPE basemap_pusher_requests_by_status_total counter\n")
	fmt.Fprintf(w, "basemap_pusher_requests_by_status_total{class=\"2xx\"} %d\n", s.metrics.status2xx.Load())
	fmt.Fprintf(w, "basemap_pusher_requests_by_status_total{class=\"4xx\"} %d\n", s.metrics.status4xx.Load())
	fmt.Fprintf(w, "basemap_pusher_requests_by_status_total{class=\"5xx\"} %d\n", s.metrics.status5xx.Load())
	fmt.Fprintf(w, "# TYPE basemap_pusher_tile_requests_total counter\n")
	fmt.Fprintf(w, "basemap_pusher_tile_requests_total %d\n", s.metrics.tiles.Load())
	fmt.Fprintf(w, "# TYPE basemap_pusher_tile_misses_total counter\n")
	fmt.Fprintf(w, "basemap_pusher_tile_misses_total %d\n", s.metrics.tileMisses.Load())
	fmt.Fprintf(w, "# TYPE basemap_pusher_auth_failures_total counter\n")
	fmt.Fprintf(w, "basemap_pusher_auth_failures_total %d\n", s.metrics.authFailures.Load())
	fmt.Fprintf(w, "# TYPE basemap_pusher_uptime_seconds gauge\n")
	fmt.Fprintf(w, "basemap_pusher_uptime_seconds %.0f\n", time.Since(s.startedAt).Seconds())
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"service": s.system.ServiceName,
		"version": s.system.ServiceVersion,
	})
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":     "ready",
		"started_at": s.startedAt,
		"scan_root":  s.system.ScanRoot,
		"sqlite":     s.system.SQLitePath,
	})
}

func (s *Server) handleSystem(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.system)
}

func (s *Server) handleDashboardJSON(w http.ResponseWriter, r *http.Request) {
	data, err := s.store.Dashboard(r.Context(), s.system, "", "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, data)
}

func (s *Server) handleBasemapsJSON(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListBasemaps(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleVectorsJSON(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListVectorAssets(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleScanSourcesJSON(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListScanSources(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleCreateScanSourceJSON(w http.ResponseWriter, r *http.Request) {
	var input basemap.CreateScanSourceInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("decode json: %w", err))
		return
	}
	item, err := s.store.EnsureScanSource(r.Context(), input)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) handleRunScanJSON(w http.ResponseWriter, r *http.Request) {
	run, err := s.store.RunScan(r.Context(), r.PathValue("code"))
	if err != nil {
		writeError(w, scanErrorStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) handleRunVectorScanJSON(w http.ResponseWriter, r *http.Request) {
	result, err := s.store.RunVectorScan(r.Context())
	if err != nil {
		writeError(w, scanErrorStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleSetDefaultVersionJSON(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.store.SetDefaultVersion(r.Context(), r.PathValue("code"), payload.Version); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleTargetsJSON(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListTargetSystems(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleCreateTargetJSON(w http.ResponseWriter, r *http.Request) {
	var input basemap.CreateTargetSystemInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	item, err := s.store.CreateTargetSystem(r.Context(), input)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) handleTokensJSON(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListTokens(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleCreateTokenJSON(w http.ResponseWriter, r *http.Request) {
	input, err := decodeTokenInput(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	item, err := s.store.CreateToken(r.Context(), input)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) handleDisableTokenJSON(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DisableToken(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleDeleteTokenJSON(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteToken(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
func (s *Server) handlePushRecordsJSON(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListPushRecords(r.Context(), 20)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleVectorPushRecordsJSON(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListVectorPushRecords(r.Context(), 20)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handlePushJSON(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		TargetCode  string `json:"target_code"`
		BasemapCode string `json:"basemap_code"`
		Version     string `json:"version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	item, err := s.store.PushBasemapVersion(r.Context(), payload.TargetCode, payload.BasemapCode, payload.Version)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusAccepted, item)
}

func (s *Server) handleVectorPushJSON(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		TargetCode string `json:"target_code"`
		VectorCode string `json:"vector_code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	item, err := s.store.PushVectorAsset(r.Context(), payload.TargetCode, payload.VectorCode)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusAccepted, item)
}

func (s *Server) handleCatalogList(w http.ResponseWriter, r *http.Request) {
	auth, ok := s.requireToken(w, r)
	if !ok {
		return
	}
	items, err := s.store.ListBasemaps(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, s.store.FilterCatalog(auth, items))
}

func (s *Server) handleCatalogGet(w http.ResponseWriter, r *http.Request) {
	auth, ok := s.requireToken(w, r)
	if !ok {
		return
	}
	item, err := s.store.GetBasemapByCode(r.Context(), r.PathValue("code"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	filtered := s.store.FilterCatalog(auth, []basemap.Basemap{item})
	if len(filtered) == 0 {
		writeError(w, http.StatusForbidden, errors.New("token has no access to this basemap"))
		return
	}
	writeJSON(w, http.StatusOK, filtered[0])
}

func (s *Server) handleVectorCatalogList(w http.ResponseWriter, r *http.Request) {
	auth, ok := s.requireToken(w, r)
	if !ok {
		return
	}
	items, err := s.store.ListVectorAssets(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, s.store.FilterVectors(auth, items))
}

func (s *Server) handleVectorCatalogGet(w http.ResponseWriter, r *http.Request) {
	auth, ok := s.requireToken(w, r)
	if !ok {
		return
	}
	item, err := s.store.GetVectorAssetByCode(r.Context(), r.PathValue("code"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	filtered := s.store.FilterVectors(auth, []basemap.VectorAsset{item})
	if len(filtered) == 0 {
		writeError(w, http.StatusForbidden, errors.New("token has no access to this vector asset"))
		return
	}
	writeJSON(w, http.StatusOK, filtered[0])
}

func (s *Server) handleTile(w http.ResponseWriter, r *http.Request) {
	auth, ok := s.requireToken(w, r)
	if !ok {
		return
	}
	version := r.PathValue("version")
	tilePath := r.PathValue("tilePath")
	item, err := s.store.ResolveTile(r.Context(), auth, r.PathValue("code"), version, tilePath)
	if err != nil && errors.Is(err, basemap.ErrBasemapVersionNotFound) && isTileZoom(version) {
		item, err = s.store.ResolveDefaultTile(r.Context(), auth, r.PathValue("code"), version+"/"+tilePath)
	}
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	w.Header().Set("Content-Type", item.ContentType)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeFile(w, r, item.FilePath)
}

func (s *Server) handleVectorDownload(w http.ResponseWriter, r *http.Request) {
	auth, ok := s.requireToken(w, r)
	if !ok {
		return
	}
	item, err := s.store.ResolveVectorDownload(r.Context(), auth, r.PathValue("code"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	w.Header().Set("Content-Type", item.ContentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", item.FileName))
	if item.Zipped {
		if err := basemap.WriteVectorZip(w, item); err != nil {
			writeError(w, http.StatusInternalServerError, err)
		}
		return
	}
	http.ServeFile(w, r, item.FilePath)
}

func (s *Server) handleGeoJSONByPath(w http.ResponseWriter, r *http.Request) {
	auth, ok := s.requireToken(w, r)
	if !ok {
		return
	}
	item, err := s.store.ResolveVectorDownloadByRef(r.Context(), auth, r.PathValue("assetPath"), "geojson")
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	w.Header().Set("Content-Type", item.ContentType)
	http.ServeFile(w, r, item.FilePath)
}

func (s *Server) handleShapeByPath(w http.ResponseWriter, r *http.Request) {
	auth, ok := s.requireToken(w, r)
	if !ok {
		return
	}
	item, err := s.store.ResolveVectorDownloadByRef(r.Context(), auth, r.PathValue("assetPath"), "shapefile", "shpzip")
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	w.Header().Set("Content-Type", item.ContentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", item.FileName))
	if item.Zipped {
		if err := basemap.WriteVectorZip(w, item); err != nil {
			writeError(w, http.StatusInternalServerError, err)
		}
		return
	}
	http.ServeFile(w, r, item.FilePath)
}

func (s *Server) handleCheckHealthJSON(w http.ResponseWriter, r *http.Request) {
	report, err := s.store.CheckHealth(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) handleTileJobsJSON(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListTileJobs(r.Context(), 50)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleCreateTileJobJSON(w http.ResponseWriter, r *http.Request) {
	var input basemap.CreateTileJobInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	job, err := s.store.CreateTileJob(r.Context(), input)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (s *Server) handleRetryPushJSON(w http.ResponseWriter, r *http.Request) {
	if err := s.store.RetryPush(r.Context(), "basemap", r.PathValue("id")); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "queued"})
}

func (s *Server) handleRetryVectorPushJSON(w http.ResponseWriter, r *http.Request) {
	if err := s.store.RetryPush(r.Context(), "vector", r.PathValue("id")); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "queued"})
}

func (s *Server) handleCheckHealthForm(w http.ResponseWriter, r *http.Request) {
	report, err := s.store.CheckHealth(r.Context())
	flash := "资源巡检已完成"
	if err == nil {
		flash = fmt.Sprintf("资源巡检完成：底图缺 %d / %d，矢量缺 %d / %d",
			report.VersionsMissing, report.VersionsChecked, report.VectorsMissing, report.VectorsChecked)
	}
	s.redirectAdmin(w, r, flash, err)
}

func (s *Server) handleCreateTileJobForm(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.redirectAdmin(w, r, "", err)
		return
	}
	zoomMin, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("zoom_min")))
	zoomMax, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("zoom_max")))
	tileSize, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("tile_size")))
	_, err := s.store.CreateTileJob(r.Context(), basemap.CreateTileJobInput{
		Code:        r.FormValue("code"),
		Name:        r.FormValue("name"),
		SourcePath:  r.FormValue("source_path"),
		TargetCode:  r.FormValue("target_code"),
		ZoomMin:     zoomMin,
		ZoomMax:     zoomMax,
		TileSize:    tileSize,
		TileFormat:  r.FormValue("tile_format"),
		CallbackURL: r.FormValue("callback_url"),
		RequestedBy: r.FormValue("requested_by"),
	})
	s.redirectAdmin(w, r, "切片任务已提交，刷新查看进度", err)
}

// handleRebuildLayerForm slices every source of a layer into one VRT mosaic,
// reusing the zoom range the layer is already published at.
func (s *Server) handleRebuildLayerForm(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.redirectAdmin(w, r, "", err)
		return
	}
	targetCode := strings.TrimSpace(r.FormValue("target_code"))
	if targetCode == "" {
		s.redirectAdmin(w, r, "", errors.New("target code is required"))
		return
	}

	sources, err := s.store.ListLayerSources(r.Context(), targetCode)
	if err != nil {
		s.redirectAdmin(w, r, "", err)
		return
	}
	if len(sources) == 0 {
		s.redirectAdmin(w, r, "", fmt.Errorf("layer %q has no recorded sources", targetCode))
		return
	}

	zoomMin := 0
	zoomMax := 18
	tileFormat := "webp"
	if items, err := s.store.ListLayerInventories(r.Context()); err == nil {
		for _, item := range items {
			if item.TargetCode == normalizeLayerCode(targetCode) {
				zoomMin, zoomMax, tileFormat = item.ZoomMin, item.ZoomMax, item.TileFormat
				break
			}
		}
	}

	paths := make([]string, 0, len(sources))
	for _, source := range sources {
		paths = append(paths, source.SourcePath)
	}
	if _, err := s.store.CreateTileJob(r.Context(), basemap.CreateTileJobInput{
		Name:        "重建 " + targetCode,
		TargetCode:  targetCode,
		SourcePaths: paths,
		ZoomMin:     zoomMin,
		ZoomMax:     zoomMax,
		TileFormat:  tileFormat,
		RequestedBy: "console",
	}); err != nil {
		s.redirectAdmin(w, r, "", err)
		return
	}
	s.redirectAdmin(w, r, fmt.Sprintf("已提交 %s 的镶嵌重建（%d 个源，级别 %d-%d）", targetCode, len(paths), zoomMin, zoomMax), nil)
}
func (s *Server) handleRetryPushForm(w http.ResponseWriter, r *http.Request) {
	err := s.store.RetryPush(r.Context(), "basemap", r.PathValue("id"))
	s.redirectAdmin(w, r, "已重新排队推送", err)
}

func (s *Server) handleRetryVectorPushForm(w http.ResponseWriter, r *http.Request) {
	err := s.store.RetryPush(r.Context(), "vector", r.PathValue("id"))
	s.redirectAdmin(w, r, "已重新排队推送", err)
}

// requireSliceToken authenticates and then checks the coarse slice capability,
// so an integration can submit jobs without holding the admin credential.
func (s *Server) requireSliceToken(w http.ResponseWriter, r *http.Request) (basemap.TokenAuth, bool) {
	auth, ok := s.requireToken(w, r)
	if !ok {
		return basemap.TokenAuth{}, false
	}
	if !auth.CanSlice() {
		writeError(w, http.StatusForbidden, errors.New("token does not carry the slice scope"))
		return basemap.TokenAuth{}, false
	}
	return auth, true
}

func (s *Server) handleSliceJobCreate(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireSliceToken(w, r); !ok {
		return
	}
	var input basemap.CreateTileJobInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	job, err := s.store.CreateTileJob(r.Context(), input)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (s *Server) handleSliceLayerSources(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireSliceToken(w, r); !ok {
		return
	}
	items, err := s.store.ListLayerSources(r.Context(), r.PathValue("code"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}
func (s *Server) handleSliceJobGet(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireSliceToken(w, r); !ok {
		return
	}
	job, err := s.store.GetTileJob(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}
func (s *Server) handleAdminPage(w http.ResponseWriter, r *http.Request) {
	data, err := s.store.Dashboard(r.Context(), s.system, r.URL.Query().Get("flash"), r.URL.Query().Get("token"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.template.Execute(w, data); err != nil {
		writeError(w, http.StatusInternalServerError, err)
	}
}

func (s *Server) handleCreateScanSourceForm(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.redirectAdmin(w, r, "", err)
		return
	}
	_, err := s.store.EnsureScanSource(r.Context(), basemap.CreateScanSourceInput{
		Code:         r.FormValue("code"),
		Name:         r.FormValue("name"),
		RootPath:     r.FormValue("root_path"),
		ManifestName: r.FormValue("manifest_name"),
	})
	s.redirectAdmin(w, r, "扫描源已保存", err)
}

func (s *Server) handleRunScanForm(w http.ResponseWriter, r *http.Request) {
	_, err := s.store.RunScan(r.Context(), r.PathValue("code"))
	s.redirectAdmin(w, r, "底图扫描已完成", err)
}

func (s *Server) handleRunVectorScanForm(w http.ResponseWriter, r *http.Request) {
	_, err := s.store.RunVectorScan(r.Context())
	s.redirectAdmin(w, r, "矢量扫描已完成", err)
}

func (s *Server) handleSetDefaultVersionForm(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.redirectAdmin(w, r, "", err)
		return
	}
	err := s.store.SetDefaultVersion(r.Context(), r.PathValue("code"), r.FormValue("version"))
	s.redirectAdmin(w, r, "默认版本已更新", err)
}

func (s *Server) handleCreateTargetForm(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.redirectAdmin(w, r, "", err)
		return
	}
	headers := map[string]string{}
	for _, line := range splitLines(r.FormValue("headers")) {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		headers[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	_, err := s.store.CreateTargetSystem(r.Context(), basemap.CreateTargetSystemInput{
		Code:            r.FormValue("code"),
		Name:            r.FormValue("name"),
		CallbackURL:     r.FormValue("callback_url"),
		CallbackMethod:  r.FormValue("callback_method"),
		CallbackHeaders: headers,
	})
	s.redirectAdmin(w, r, "目标系统已保存", err)
}

func (s *Server) handleCreateTokenForm(w http.ResponseWriter, r *http.Request) {
	input, err := decodeTokenInputFromForm(r)
	if err != nil {
		s.redirectAdmin(w, r, "", err)
		return
	}
	item, err := s.store.CreateToken(r.Context(), input)
	if err != nil {
		s.redirectAdmin(w, r, "", err)
		return
	}
	s.renderAdmin(w, r, "访问令牌已创建", item.Token, nil)
}

func (s *Server) handleDeleteTokenForm(w http.ResponseWriter, r *http.Request) {
	err := s.store.DeleteToken(r.Context(), r.PathValue("id"))
	s.redirectAdmin(w, r, "访问令牌已删除", err)
}
func (s *Server) handleDisableTokenForm(w http.ResponseWriter, r *http.Request) {
	err := s.store.DisableToken(r.Context(), r.PathValue("id"))
	s.redirectAdmin(w, r, "访问令牌已停用", err)
}

func (s *Server) handlePushForm(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.redirectAdmin(w, r, "", err)
		return
	}
	_, err := s.store.PushBasemapVersion(r.Context(), r.FormValue("target_code"), r.FormValue("basemap_code"), r.FormValue("version"))
	s.redirectAdmin(w, r, "底图推送已提交，稍后刷新查看结果", err)
}

func (s *Server) handleVectorPushForm(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.redirectAdmin(w, r, "", err)
		return
	}
	_, err := s.store.PushVectorAsset(r.Context(), r.FormValue("target_code"), r.FormValue("vector_code"))
	s.redirectAdmin(w, r, "矢量推送已提交，稍后刷新查看结果", err)
}

func (s *Server) requireToken(w http.ResponseWriter, r *http.Request) (basemap.TokenAuth, bool) {
	rawToken := extractToken(r)
	auth, err := s.store.AuthorizeToken(r.Context(), rawToken)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return basemap.TokenAuth{}, false
	}
	return auth, true
}

func extractToken(r *http.Request) string {
	if value := strings.TrimSpace(r.Header.Get("X-Access-Token")); value != "" {
		return value
	}
	if header := strings.TrimSpace(r.Header.Get("Authorization")); header != "" {
		if strings.HasPrefix(strings.ToLower(header), "bearer ") {
			return strings.TrimSpace(header[7:])
		}
	}
	for _, key := range []string{"token", "access_token"} {
		if value := strings.TrimSpace(r.URL.Query().Get(key)); value != "" {
			return value
		}
	}
	return ""
}

func decodeTokenInput(r *http.Request) (basemap.CreateTokenInput, error) {
	var payload struct {
		Name         string   `json:"name"`
		BasemapCodes []string `json:"basemap_codes"`
		VectorCodes  []string `json:"vector_codes"`
		Scopes       []string `json:"scopes"`
		ExpiresAt    string   `json:"expires_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		return basemap.CreateTokenInput{}, err
	}
	input := basemap.CreateTokenInput{
		Name:         payload.Name,
		BasemapCodes: payload.BasemapCodes,
		VectorCodes:  payload.VectorCodes,
		Scopes:       payload.Scopes,
	}
	switch strings.ToLower(strings.TrimSpace(payload.ExpiresAt)) {
	case "never", "0":
		input.NoExpiry = true
	case "":
	default:
		parsed, err := time.Parse(time.RFC3339, payload.ExpiresAt)
		if err != nil {
			return basemap.CreateTokenInput{}, fmt.Errorf("parse expires_at: %w", err)
		}
		input.ExpiresAt = &parsed
	}
	return input, nil
}

func decodeTokenInputFromForm(r *http.Request) (basemap.CreateTokenInput, error) {
	if err := r.ParseForm(); err != nil {
		return basemap.CreateTokenInput{}, err
	}
	input := basemap.CreateTokenInput{
		Name:         r.FormValue("name"),
		BasemapCodes: splitCSV(r.FormValue("basemap_codes")),
		VectorCodes:  splitCSV(r.FormValue("vector_codes")),
		Scopes:       splitCSV(r.FormValue("scopes")),
	}
	if value := strings.TrimSpace(r.FormValue("expires_at")); value != "" {
		switch strings.ToLower(value) {
		case "never", "0":
			input.NoExpiry = true
		default:
			if minutes, err := strconv.Atoi(value); err == nil && minutes > 0 {
				expiresAt := time.Now().UTC().Add(time.Duration(minutes) * time.Minute)
				input.ExpiresAt = &expiresAt
			}
		}
	}
	return input, nil
}

func splitCSV(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	})
	result := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field != "" {
			result = append(result, field)
		}
	}
	return result
}

func splitLines(value string) []string {
	raw := strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func isTileZoom(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func statusText(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		return "-"
	case "active", "enabled":
		return "启用"
	case "disabled":
		return "停用"
	case "ready":
		return "就绪"
	case "succeeded":
		return "成功"
	case "failed":
		return "失败"
	case "pending":
		return "待处理"
	case "retrying":
		return "重试中"
	case "running":
		return "运行中"
	case "processing":
		return "处理中"
	case "current":
		return "当前"
	case "error":
		return "异常"
	default:
		return value
	}
}

func statusTone(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "active", "enabled", "ready", "succeeded", "current":
		return "success"
	case "pending", "running", "processing", "retrying":
		return "warning"
	case "disabled", "failed", "error", "expired", "invalid":
		return "danger"
	default:
		return "neutral"
	}
}

func formatBytes(size int64) string {
	if size < 0 {
		return "-"
	}
	units := []string{"B", "KB", "MB", "GB", "TB"}
	value := float64(size)
	unit := 0
	for value >= 1024 && unit < len(units)-1 {
		value /= 1024
		unit++
	}
	if unit == 0 {
		return fmt.Sprintf("%d %s", size, units[unit])
	}
	return fmt.Sprintf("%.1f %s", value, units[unit])
}

// scanErrorStatus maps scan failures onto HTTP status codes: a busy scanner is
// a conflict, an unknown source is a 404, and anything else is a server fault.
// normalizeLayerCode mirrors the store's basemap code normalization so the
// console can match a layer against its inventory entry.
func normalizeLayerCode(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
func scanErrorStatus(err error) int {
	switch {
	case errors.Is(err, basemap.ErrScanInProgress):
		return http.StatusConflict
	case errors.Is(err, basemap.ErrScanSourceNotFound):
		return http.StatusNotFound
	case errors.Is(err, basemap.ErrScanSourceDisabled):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}
func localizeAdminError(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.TrimSpace(err.Error())
	quoted := quotedSegments(msg)

	switch {
	case msg == "scan source code is required":
		return "扫描源编码不能为空"
	case msg == "scan source root path is required":
		return "扫描根目录不能为空"
	case strings.HasPrefix(msg, "resolve root path:"):
		return "扫描根目录解析失败：" + strings.TrimSpace(strings.TrimPrefix(msg, "resolve root path:"))
	case len(quoted) == 1 && strings.HasPrefix(msg, `scan source "`) && strings.HasSuffix(msg, `" not found`):
		return fmt.Sprintf("未找到扫描源：%s", quoted[0])
	case len(quoted) == 1 && strings.HasPrefix(msg, `scan source "`) && strings.HasSuffix(msg, `" is disabled`):
		return fmt.Sprintf("扫描源已停用：%s", quoted[0])
	case strings.HasPrefix(msg, "read scan root:"):
		return "读取扫描目录失败：" + strings.TrimSpace(strings.TrimPrefix(msg, "read scan root:"))
	case strings.HasPrefix(msg, "scan root ") && strings.HasSuffix(msg, " is not a directory"):
		return "配置的扫描根路径不是目录：" + strings.TrimSuffix(strings.TrimPrefix(msg, "scan root "), " is not a directory")
	case msg == "target system code is required":
		return "目标系统编码不能为空"
	case msg == "callback url is required":
		return "回调地址不能为空"
	case len(quoted) == 1 && strings.HasPrefix(msg, `target system "`) && strings.HasSuffix(msg, `" not found`):
		return fmt.Sprintf("未找到目标系统：%s", quoted[0])
	case len(quoted) == 1 && strings.HasPrefix(msg, `target system "`) && strings.HasSuffix(msg, `" is disabled`):
		return fmt.Sprintf("目标系统已停用：%s", quoted[0])
	case msg == "token name is required":
		return "令牌名称不能为空"
	case msg == "at least one basemap code, vector code, or the slice scope is required":
		return "至少填写一个底图编码、矢量资源，或勾选切片能力"
	case msg == "no valid basemap codes provided":
		return "未提供有效的底图编码"
	case len(quoted) == 1 && strings.HasPrefix(msg, `token "`) && strings.HasSuffix(msg, `" not found`):
		return fmt.Sprintf("未找到访问令牌：%s", quoted[0])
	case msg == "basemap code and version are required":
		return "底图编码和版本不能为空"
	case len(quoted) == 1 && strings.HasPrefix(msg, `basemap "`) && strings.HasSuffix(msg, `" not found`):
		return fmt.Sprintf("未找到底图资源：%s", quoted[0])
	case len(quoted) == 2 && strings.HasPrefix(msg, `version "`) && strings.Contains(msg, `" not found for basemap "`):
		return fmt.Sprintf("底图资源 %s 未找到版本 %s", quoted[1], quoted[0])
	case len(quoted) == 1 && strings.HasPrefix(msg, `vector asset "`) && strings.HasSuffix(msg, `" not found`):
		return fmt.Sprintf("未找到矢量资源：%s", quoted[0])
	case len(quoted) == 1 && strings.HasPrefix(msg, `vector asset "`) && strings.HasSuffix(msg, `" is not available in requested format`):
		return fmt.Sprintf("矢量资源当前不可按所选格式提供：%s", quoted[0])
	default:
		return msg
	}
}

func quotedSegments(value string) []string {
	parts := strings.Split(value, `"`)
	result := make([]string, 0, len(parts)/2)
	for i := 1; i < len(parts); i += 2 {
		result = append(result, parts[i])
	}
	return result
}

// redirectAdmin sends the browser back to the console with a flash message. It
// deliberately never carries a secret in the URL.
func (s *Server) redirectAdmin(w http.ResponseWriter, r *http.Request, flash string, err error) {
	if err != nil {
		flash = "操作失败：" + localizeAdminError(err)
	}
	location := "/admin/"
	if strings.TrimSpace(flash) != "" {
		values := url.Values{}
		values.Set("flash", flash)
		location += "?" + values.Encode()
	}
	http.Redirect(w, r, location, http.StatusSeeOther)
}

// renderAdmin answers the current POST with the console HTML so one-time
// secrets (a freshly minted token) stay out of the URL, history and logs.
func (s *Server) renderAdmin(w http.ResponseWriter, r *http.Request, flash string, token string, err error) {
	if err != nil {
		flash = "操作失败：" + localizeAdminError(err)
	}
	data, dataErr := s.store.Dashboard(r.Context(), s.system, flash, token)
	if dataErr != nil {
		writeError(w, http.StatusInternalServerError, dataErr)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if execErr := s.template.Execute(w, data); execErr != nil {
		writeError(w, http.StatusInternalServerError, execErr)
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]any{
		"error": err.Error(),
	})
}
