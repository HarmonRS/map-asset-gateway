package basemapapi

import (
	"bytes"
	"context"
	"image/png"
	"io"
	"net/http"
	"path/filepath"
	"testing"

	"tile-server/api-go/internal/basemap"
)

func TestCoverageHintTilesBelowMinimumZoom(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	tileRoot := filepath.Join(root, "tiles")
	writeFile(t, filepath.Join(tileRoot, "uav", "2", "1", "1.png"), "tile")

	store, err := basemap.OpenStore(basemap.StoreConfig{
		SQLitePath:    filepath.Join(root, "basemap.db"),
		ScanRoot:      tileRoot,
		APIBaseURL:    "http://127.0.0.1:8910",
		TileBaseURL:   "http://127.0.0.1:8910",
		VectorBaseURL: "http://127.0.0.1:8910",
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.RunScan(ctx, "local-basemaps"); err != nil {
		t.Fatalf("scan: %v", err)
	}
	token, err := store.CreateToken(ctx, basemap.CreateTokenInput{
		Name:         "hint-reader",
		BasemapCodes: []string{"uav"},
		Scopes:       []string{"read"},
	})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	httpServer := newTestServer(t, store, Options{})

	response, err := http.Get(httpServer.URL + "/tiles/uav/current/1/0/0.png?token=" + token.Token)
	if err != nil {
		t.Fatalf("request hint tile: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("expected hint 200, got %d: %s", response.StatusCode, body)
	}
	if got := response.Header.Get("Content-Type"); got != "image/png" {
		t.Fatalf("expected image/png hint, got %q", got)
	}
	if got := response.Header.Get("X-Coverage-Hint"); got != "1" {
		t.Fatalf("expected X-Coverage-Hint=1, got %q", got)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read hint body: %v", err)
	}
	if _, err := png.Decode(bytes.NewReader(body)); err != nil {
		t.Fatalf("hint body is not a PNG: %v", err)
	}

	response, err = http.Get(httpServer.URL + "/tiles/uav/current/1/1/0.png?token=" + token.Token)
	if err != nil {
		t.Fatalf("request outside-coverage hint: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 outside coverage, got %d", response.StatusCode)
	}

	response, err = http.Get(httpServer.URL + "/tiles/uav/current/2/1/1.png?token=" + token.Token)
	if err != nil {
		t.Fatalf("request real tile: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("expected real tile 200, got %d", response.StatusCode)
	}
	if got := response.Header.Get("X-Coverage-Hint"); got != "" {
		t.Fatalf("real tile must not be marked as a hint, got %q", got)
	}
}
