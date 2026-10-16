package basemap

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func manifestBody(code, name string) string {
	return `{"code":"` + code + `","name":"` + name + `","type":"xyz","version":"current","tile_format":"png","root_path":"tiles","is_default":true}`
}

// The manifest lookup must not descend into the tile payload: a manifest nested
// inside another basemap's "tiles" subtree must be ignored.
func TestFindManifestVersionsPrunesTilePayload(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "demo", "manifest.json"), manifestBody("demo", "Demo"))
	writeTestFile(t, filepath.Join(root, "demo", "tiles", "0", "0", "0.png"), "tile")
	writeTestFile(t, filepath.Join(root, "demo", "tiles", "nested", "manifest.json"), manifestBody("nested", "Nested"))

	store := &Store{}
	items, covered, err := store.findManifestVersions(ScanSource{RootPath: root, ManifestName: "manifest.json"})
	if err != nil {
		t.Fatalf("find manifests: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 manifest, got %d: %+v", len(items), items)
	}
	if items[0].BasemapCode != "demo" {
		t.Fatalf("expected code demo, got %q", items[0].BasemapCode)
	}
	if _, ok := covered["demo"]; !ok {
		t.Fatalf("expected demo to be marked as covered, got %v", covered)
	}
}

func TestIsNumericDirName(t *testing.T) {
	for _, name := range []string{"0", "14", "007"} {
		if !isNumericDirName(name) {
			t.Fatalf("expected %q to be numeric", name)
		}
	}
	for _, name := range []string{"", "gaode-image", "2026-04", "tiles", "1a"} {
		if isNumericDirName(name) {
			t.Fatalf("expected %q to be non-numeric", name)
		}
	}
}

func TestSQLiteDSNEnablesWALAndForeignKeys(t *testing.T) {
	root := t.TempDir()
	store, err := OpenStore(StoreConfig{SQLitePath: filepath.Join(root, "basemap.db")})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	var mode string
	if err := store.db.QueryRow("PRAGMA journal_mode;").Scan(&mode); err != nil {
		t.Fatalf("read journal_mode: %v", err)
	}
	if !strings.EqualFold(mode, "wal") {
		t.Fatalf("expected WAL journal mode, got %q", mode)
	}

	var foreignKeys int
	if err := store.db.QueryRow("PRAGMA foreign_keys;").Scan(&foreignKeys); err != nil {
		t.Fatalf("read foreign_keys: %v", err)
	}
	if foreignKeys != 1 {
		t.Fatalf("expected foreign_keys=1, got %d", foreignKeys)
	}
}

func TestAuthorizeTokenThrottlesLastUsedWrite(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	tileRoot := filepath.Join(root, "tiles")
	writeTestFile(t, filepath.Join(tileRoot, "google_image", "0", "0", "0.webp"), "tile")

	store, err := OpenStore(StoreConfig{
		SQLitePath:  filepath.Join(root, "basemap.db"),
		ScanRoot:    tileRoot,
		APIBaseURL:  "http://127.0.0.1:8910",
		TileBaseURL: "http://127.0.0.1:8910",
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	if _, err := store.RunScan(ctx, defaultScanSourceCode); err != nil {
		t.Fatalf("run scan: %v", err)
	}
	created, err := store.CreateToken(ctx, CreateTokenInput{Name: "throttle", BasemapCodes: []string{"google_image"}})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	// Mark the token as unused long ago so the next authorization refreshes it.
	stale := toRFC3339(nowUTC().Add(-10 * time.Minute))
	if _, err := store.db.ExecContext(ctx, `UPDATE service_tokens SET last_used_at = ? WHERE id = ?`, stale, created.Meta.ID); err != nil {
		t.Fatalf("seed stale last_used_at: %v", err)
	}
	if _, err := store.AuthorizeToken(ctx, created.Token); err != nil {
		t.Fatalf("authorize token: %v", err)
	}
	first := readLastUsedAt(t, store, created.Meta.ID)
	if first == stale {
		t.Fatalf("expected last_used_at to be refreshed, still %q", first)
	}

	if _, err := store.AuthorizeToken(ctx, created.Token); err != nil {
		t.Fatalf("authorize token again: %v", err)
	}
	second := readLastUsedAt(t, store, created.Meta.ID)
	if second != first {
		t.Fatalf("expected last_used_at to be throttled, got %q then %q", first, second)
	}
}

func readLastUsedAt(t *testing.T, store *Store, tokenID string) string {
	t.Helper()
	var value string
	if err := store.db.QueryRow(`SELECT last_used_at FROM service_tokens WHERE id = ?`, tokenID).Scan(&value); err != nil {
		t.Fatalf("read last_used_at: %v", err)
	}
	return value
}

func TestGetBasemapByCodeLoadsVersions(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	tileRoot := filepath.Join(root, "tiles")
	writeTestFile(t, filepath.Join(tileRoot, "google_image", "0", "0", "0.webp"), "tile")
	writeTestFile(t, filepath.Join(tileRoot, "google_image", "1", "0", "0.webp"), "tile")

	store, err := OpenStore(StoreConfig{
		SQLitePath:  filepath.Join(root, "basemap.db"),
		ScanRoot:    tileRoot,
		APIBaseURL:  "http://127.0.0.1:8910",
		TileBaseURL: "http://127.0.0.1:8910",
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	if _, err := store.RunScan(ctx, defaultScanSourceCode); err != nil {
		t.Fatalf("run scan: %v", err)
	}

	item, err := store.GetBasemapByCode(ctx, "google_image")
	if err != nil {
		t.Fatalf("get basemap: %v", err)
	}
	if item.Code != "google-image" {
		t.Fatalf("unexpected code: %q", item.Code)
	}
	if len(item.Versions) != 1 {
		t.Fatalf("expected 1 version, got %d", len(item.Versions))
	}
	if item.Default == nil || item.Default.Version != "current" {
		t.Fatalf("expected default version current, got %+v", item.Default)
	}
	if item.Default.MinZoom != 0 || item.Default.MaxZoom != 1 {
		t.Fatalf("unexpected zoom range: %d-%d", item.Default.MinZoom, item.Default.MaxZoom)
	}
	if _, err := store.GetBasemapByCode(ctx, "missing"); err == nil {
		t.Fatal("expected missing basemap to error")
	}
}
