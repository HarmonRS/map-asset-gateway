package basemapapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"tile-server/api-go/internal/basemap"
)

func newSliceServer(t *testing.T) (*basemap.Store, *httptest.Server) {
	t.Helper()
	root := t.TempDir()
	tileRoot := filepath.Join(root, "tiles")
	if err := os.MkdirAll(tileRoot, 0o755); err != nil {
		t.Fatalf("mkdir tile root: %v", err)
	}
	source := filepath.Join(root, "input.tif")
	writeFile(t, source, "fake raster")
	// A scanned basemap gives the negative test case something to grant.
	writeFile(t, filepath.Join(tileRoot, "google_image", "0", "0", "0.webp"), "tile")

	store, err := basemap.OpenStore(basemap.StoreConfig{
		SQLitePath:     filepath.Join(root, "basemap.db"),
		ScanRoot:       tileRoot,
		VectorScanRoot: filepath.Join(root, "vectors"),
		APIBaseURL:     "http://127.0.0.1:8910",
		TileBaseURL:    "http://127.0.0.1:8910",
		VectorBaseURL:  "http://127.0.0.1:8910",
		TileJobCommand: "definitely-not-a-real-binary {input} {output}",
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.RunScan(context.Background(), "local-basemaps"); err != nil {
		t.Fatalf("scan: %v", err)
	}

	return store, newTestServerRaw(t, store)
}

func newTestServerRaw(t *testing.T, store *basemap.Store) *httptest.Server {
	t.Helper()
	server, err := New(store, basemap.SystemInfo{ServiceName: "basemap-pusher"}, Options{})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(httpServer.Close)
	return httpServer
}

func postJob(t *testing.T, url, token string, body map[string]any) *http.Response {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	request, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	return response
}

func TestSliceJobRequiresSliceScope(t *testing.T) {
	ctx := context.Background()
	store, httpServer := newSliceServer(t)
	source := filepath.Join(filepath.Dir(store.ScanRoot()), "input.tif")

	readOnly, err := store.CreateToken(ctx, basemap.CreateTokenInput{
		Name:         "read-only",
		BasemapCodes: []string{"google_image"},
		Scopes:       []string{"read"},
	})
	if err != nil {
		t.Fatalf("create read-only token: %v", err)
	}
	if _, err := store.CreateToken(ctx, basemap.CreateTokenInput{Name: "empty"}); err == nil {
		t.Fatal("expected a token with neither grants nor slice scope to be rejected")
	}

	response := postJob(t, httpServer.URL+"/api/v1/slice/jobs", readOnly.Token, map[string]any{
		"source_path": source,
		"target_code": "demo",
	})
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for a read-only token, got %d", response.StatusCode)
	}
}

func TestSliceJobSubmitAndPoll(t *testing.T) {
	ctx := context.Background()
	store, httpServer := newSliceServer(t)
	source := filepath.Join(filepath.Dir(store.ScanRoot()), "input.tif")

	slicer, err := store.CreateToken(ctx, basemap.CreateTokenInput{
		Name:   "integration",
		Scopes: []string{"slice"},
	})
	if err != nil {
		t.Fatalf("create slicer token: %v", err)
	}

	body := map[string]any{
		"code":        "integration-job",
		"source_path": source,
		"target_code": "demo",
		"zoom_min":    0,
		"zoom_max":    2,
	}
	response := postJob(t, httpServer.URL+"/api/v1/slice/jobs", slicer.Token, body)
	if response.StatusCode != http.StatusAccepted {
		response.Body.Close()
		t.Fatalf("expected 202, got %d", response.StatusCode)
	}
	var job basemap.TileJob
	if err := json.NewDecoder(response.Body).Decode(&job); err != nil {
		t.Fatalf("decode: %v", err)
	}
	response.Body.Close()

	// Re-submitting the same code must not queue a second job.
	again := postJob(t, httpServer.URL+"/api/v1/slice/jobs", slicer.Token, body)
	again.Body.Close()
	if again.StatusCode != http.StatusAccepted {
		t.Fatalf("expected the repeat submit to be accepted, got %d", again.StatusCode)
	}

	request, _ := http.NewRequest(http.MethodGet, httpServer.URL+"/api/v1/slice/jobs/"+job.ID, nil)
	request.Header.Set("Authorization", "Bearer "+slicer.Token)
	polled, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	defer polled.Body.Close()
	if polled.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from the poll endpoint, got %d", polled.StatusCode)
	}
	var fetched basemap.TileJob
	if err := json.NewDecoder(polled.Body).Decode(&fetched); err != nil {
		t.Fatalf("decode polled job: %v", err)
	}
	if fetched.ID != job.ID {
		t.Fatalf("polled %s, submitted %s", fetched.ID, job.ID)
	}

	jobs, err := store.ListTileJobs(ctx, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("expected one job after a repeated submit, got %d", len(jobs))
	}
}

func TestSliceJobRejectsAnonymous(t *testing.T) {
	_, httpServer := newSliceServer(t)
	request, _ := http.NewRequest(http.MethodGet, httpServer.URL+"/api/v1/slice/jobs/whatever", nil)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without a token, got %d", response.StatusCode)
	}
}
