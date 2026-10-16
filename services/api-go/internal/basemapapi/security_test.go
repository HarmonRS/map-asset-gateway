package basemapapi

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tile-server/api-go/internal/basemap"
)

func newTestStore(t *testing.T, root string) *basemap.Store {
	t.Helper()
	tileRoot := filepath.Join(root, "tiles")
	writeFile(t, filepath.Join(tileRoot, "google_image", "0", "0", "0.webp"), "tile")
	vectorRoot := filepath.Join(root, "vectors")

	store, err := basemap.OpenStore(basemap.StoreConfig{
		SQLitePath:     filepath.Join(root, "basemap.db"),
		ScanRoot:       tileRoot,
		VectorScanRoot: vectorRoot,
		APIBaseURL:     "http://127.0.0.1:8910",
		TileBaseURL:    "http://127.0.0.1:8910",
		VectorBaseURL:  "http://127.0.0.1:8910",
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if _, err := store.RunScan(context.Background(), "local-basemaps"); err != nil {
		t.Fatalf("run scan: %v", err)
	}
	return store
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
}

func newTestServer(t *testing.T, store *basemap.Store, options Options) *httptest.Server {
	t.Helper()
	server, err := New(store, basemap.SystemInfo{ServiceName: "basemap-pusher"}, options)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(httpServer.Close)
	return httpServer
}

func basicAuthHeader(user, password string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+password))
}

func TestAdminRequiresBasicAuth(t *testing.T) {
	store := newTestStore(t, t.TempDir())
	httpServer := newTestServer(t, store, Options{Admin: AdminAuth{User: "ops", Password: "s3cret"}})

	for _, path := range []string{"/api/v1/admin/basemaps", "/admin/"} {
		response, err := http.Get(httpServer.URL + path)
		if err != nil {
			t.Fatalf("unauthorized GET %s: %v", path, err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected 401 for anonymous %s, got %d", path, response.StatusCode)
		}

		request, err := http.NewRequest(http.MethodGet, httpServer.URL+path, nil)
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		request.Header.Set("Authorization", basicAuthHeader("ops", "s3cret"))
		response, err = http.DefaultClient.Do(request)
		if err != nil {
			t.Fatalf("authorized GET %s: %v", path, err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 for authorized %s, got %d", path, response.StatusCode)
		}
	}
}

func TestAdminRejectsWrongBasicAuth(t *testing.T) {
	store := newTestStore(t, t.TempDir())
	httpServer := newTestServer(t, store, Options{Admin: AdminAuth{User: "ops", Password: "s3cret"}})

	request, _ := http.NewRequest(http.MethodGet, httpServer.URL+"/api/v1/admin/basemaps", nil)
	request.Header.Set("Authorization", basicAuthHeader("ops", "wrong"))
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", response.StatusCode)
	}
}

func TestAdminAcceptsBearerToken(t *testing.T) {
	store := newTestStore(t, t.TempDir())
	httpServer := newTestServer(t, store, Options{Admin: AdminAuth{Token: "admin-token"}})

	request, _ := http.NewRequest(http.MethodGet, httpServer.URL+"/api/v1/admin/basemaps", nil)
	request.Header.Set("Authorization", "Bearer admin-token")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 with bearer token, got %d", response.StatusCode)
	}

	request, _ = http.NewRequest(http.MethodGet, httpServer.URL+"/api/v1/admin/basemaps", nil)
	request.Header.Set("Authorization", "Bearer nope")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 with a wrong bearer token, got %d", response.StatusCode)
	}
}

func TestAdminOpenWhenUnconfigured(t *testing.T) {
	store := newTestStore(t, t.TempDir())
	httpServer := newTestServer(t, store, Options{})

	response, err := http.Get(httpServer.URL + "/api/v1/admin/basemaps")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("expected the admin API to be open without credentials, got %d", response.StatusCode)
	}
}

func TestCatalogStillRequiresTokenEvenWithAdminAuth(t *testing.T) {
	store := newTestStore(t, t.TempDir())
	httpServer := newTestServer(t, store, Options{Admin: AdminAuth{User: "ops", Password: "s3cret"}})

	response, err := http.Get(httpServer.URL + "/api/v1/catalog/basemaps")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected the catalog to still require a service token, got %d", response.StatusCode)
	}
}

func TestCORSAllowlist(t *testing.T) {
	store := newTestStore(t, t.TempDir())
	httpServer := newTestServer(t, store, Options{CORSAllowedOrigins: []string{"https://maps.example.com"}})

	allowed, _ := http.NewRequest(http.MethodGet, httpServer.URL+"/health", nil)
	allowed.Header.Set("Origin", "https://maps.example.com")
	response, err := http.DefaultClient.Do(allowed)
	if err != nil {
		t.Fatalf("allowed origin: %v", err)
	}
	response.Body.Close()
	if got := response.Header.Get("Access-Control-Allow-Origin"); got != "https://maps.example.com" {
		t.Fatalf("expected the allowlisted origin to be reflected, got %q", got)
	}

	blocked, _ := http.NewRequest(http.MethodGet, httpServer.URL+"/health", nil)
	blocked.Header.Set("Origin", "https://evil.example.com")
	response, err = http.DefaultClient.Do(blocked)
	if err != nil {
		t.Fatalf("blocked origin: %v", err)
	}
	response.Body.Close()
	if got := response.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("expected no CORS header for an unlisted origin, got %q", got)
	}
}

func TestCreateTokenFormKeepsTokenOutOfURL(t *testing.T) {
	store := newTestStore(t, t.TempDir())
	httpServer := newTestServer(t, store, Options{})

	form := url.Values{}
	form.Set("name", "console")
	form.Set("basemap_codes", "google_image")
	request, err := http.NewRequest(http.MethodPost, httpServer.URL+"/admin/tokens", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("post form: %v", err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)

	if location := response.Header.Get("Location"); strings.Contains(location, "bmp_") {
		t.Fatalf("token leaked into the redirect URL: %s", location)
	}
	if !strings.Contains(string(body), "bmp_") {
		t.Fatalf("expected the new token to be rendered in the response body")
	}
}

func TestMetricsEndpointCountsTileMisses(t *testing.T) {
	store := newTestStore(t, t.TempDir())
	httpServer := newTestServer(t, store, Options{})

	created, err := store.CreateToken(context.Background(), basemap.CreateTokenInput{
		Name:         "metrics",
		BasemapCodes: []string{"google_image"},
	})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	response, err := http.Get(httpServer.URL + "/tiles/google_image/current/9/9/9.png?token=" + created.Token)
	if err != nil {
		t.Fatalf("tile request: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for a missing tile, got %d", response.StatusCode)
	}

	response, err = http.Get(httpServer.URL + "/metrics")
	if err != nil {
		t.Fatalf("metrics request: %v", err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	text := string(body)
	for _, expected := range []string{
		"basemap_pusher_requests_total",
		"basemap_pusher_tile_requests_total 1",
		"basemap_pusher_tile_misses_total 1",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected /metrics to contain %q, got:\n%s", expected, text)
		}
	}
}

func TestScanErrorStatusMapping(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{basemap.ErrScanInProgress, http.StatusConflict},
		{basemap.ErrScanSourceNotFound, http.StatusNotFound},
		{basemap.ErrScanSourceDisabled, http.StatusBadRequest},
		{io.ErrUnexpectedEOF, http.StatusInternalServerError},
	}
	for _, tc := range cases {
		if got := scanErrorStatus(tc.err); got != tc.want {
			t.Fatalf("scanErrorStatus(%v) = %d, want %d", tc.err, got, tc.want)
		}
	}
}

func TestAdminIsLoopbackOnlyByDefault(t *testing.T) {
	store := newTestStore(t, t.TempDir())
	server, err := New(store, basemap.SystemInfo{ServiceName: "basemap-pusher"}, Options{})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	handler := server.Handler()

	call := func(path, remote string) int {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.RemoteAddr = remote
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder.Code
	}

	if got := call("/api/v1/admin/basemaps", "127.0.0.1:5555"); got != http.StatusOK {
		t.Fatalf("expected local admin access, got %d", got)
	}
	if got := call("/api/v1/admin/basemaps", "192.168.1.50:5555"); got != http.StatusForbidden {
		t.Fatalf("expected a remote admin request to be refused, got %d", got)
	}

	// The point of the split: tile traffic still reaches the service from the
	// LAN, it just has to carry a valid token.
	if got := call("/tiles/demo/current/0/0/0.png", "192.168.1.50:5555"); got != http.StatusUnauthorized {
		t.Fatalf("expected the tile route to stay reachable from the LAN (401), got %d", got)
	}
	if got := call("/health", "192.168.1.50:5555"); got != http.StatusOK {
		t.Fatalf("expected health to stay reachable, got %d", got)
	}
}

func TestAdminAllowRemoteOptIn(t *testing.T) {
	store := newTestStore(t, t.TempDir())
	server, err := New(store, basemap.SystemInfo{ServiceName: "basemap-pusher"}, Options{
		Admin: AdminAuth{AllowRemote: true},
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/basemaps", nil)
	request.RemoteAddr = "192.168.1.50:5555"
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected ADMIN_ALLOW_REMOTE to permit remote admin calls, got %d", recorder.Code)
	}
}
