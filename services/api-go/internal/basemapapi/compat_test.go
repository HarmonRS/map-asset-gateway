package basemapapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"tile-server/api-go/internal/basemap"
)

func TestCompatibilityRoutes(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	tileRoot := filepath.Join(root, "tiles")
	vectorRoot := filepath.Join(root, "vectors")
	sqlitePath := filepath.Join(root, "basemap.db")

	if err := os.MkdirAll(filepath.Join(tileRoot, "google_image", "0", "0"), 0o755); err != nil {
		t.Fatalf("mkdir tile root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tileRoot, "google_image", "0", "0", "0.webp"), []byte("tile"), 0o644); err != nil {
		t.Fatalf("write tile: %v", err)
	}
	if err := os.MkdirAll(vectorRoot, 0o755); err != nil {
		t.Fatalf("mkdir vector root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(vectorRoot, "全国行政区.geojson"), []byte(`{"type":"FeatureCollection","features":[]}`), 0o644); err != nil {
		t.Fatalf("write geojson: %v", err)
	}

	store, err := basemap.OpenStore(basemap.StoreConfig{
		SQLitePath:     sqlitePath,
		ScanRoot:       tileRoot,
		VectorScanRoot: vectorRoot,
		APIBaseURL:     "http://127.0.0.1:8910",
		TileBaseURL:    "http://127.0.0.1:8910",
		VectorBaseURL:  "http://127.0.0.1:8910",
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
	}()

	if _, err := store.RunScan(ctx, "local-basemaps"); err != nil {
		t.Fatalf("run scan: %v", err)
	}
	if _, err := store.RunVectorScan(ctx); err != nil {
		t.Fatalf("run vector scan: %v", err)
	}
	token, err := store.CreateToken(ctx, basemap.CreateTokenInput{
		Name:         "compat",
		BasemapCodes: []string{"google_image"},
		VectorCodes:  []string{"全国行政区.geojson"},
	})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	server, err := New(store, basemap.SystemInfo{
		ServiceName:    "basemap-pusher",
		ServiceVersion: "test",
		APIBaseURL:     "http://127.0.0.1:8910",
		TileBaseURL:    "http://127.0.0.1:8910",
		VectorBaseURL:  "http://127.0.0.1:8910",
		SQLitePath:     sqlitePath,
		ScanRoot:       tileRoot,
		VectorScanRoot: vectorRoot,
	}, Options{})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	response, err := http.Get(httpServer.URL + "/tiles/google_image/0/0/0.webp?token=" + token.Token)
	if err != nil {
		t.Fatalf("request default tile route: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("unexpected tile status: %d", response.StatusCode)
	}
	tileBody, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read tile body: %v", err)
	}
	if string(tileBody) != "tile" {
		t.Fatalf("unexpected tile body: %q", string(tileBody))
	}

	response, err = http.Get(httpServer.URL + "/geojson/%E5%85%A8%E5%9B%BD%E8%A1%8C%E6%94%BF%E5%8C%BA.geojson?token=" + token.Token)
	if err != nil {
		t.Fatalf("request geojson route: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("unexpected geojson status: %d", response.StatusCode)
	}
}
