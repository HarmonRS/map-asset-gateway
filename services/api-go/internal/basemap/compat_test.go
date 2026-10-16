package basemap

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestInsarCompatibilityHelpers(t *testing.T) {
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

	if err := os.MkdirAll(filepath.Join(vectorRoot, "landuse"), 0o755); err != nil {
		t.Fatalf("mkdir vector root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(vectorRoot, "全国行政区.geojson"), []byte(`{"type":"FeatureCollection","features":[]}`), 0o644); err != nil {
		t.Fatalf("write geojson: %v", err)
	}
	for _, name := range []string{"parcel.shp", "parcel.dbf", "parcel.shx"} {
		if err := os.WriteFile(filepath.Join(vectorRoot, "landuse", name), []byte(name), 0o644); err != nil {
			t.Fatalf("write shapefile part %s: %v", name, err)
		}
	}

	store, err := OpenStore(StoreConfig{
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

	if _, err := store.RunScan(ctx, defaultScanSourceCode); err != nil {
		t.Fatalf("run basemap scan: %v", err)
	}
	if _, err := store.RunVectorScan(ctx); err != nil {
		t.Fatalf("run vector scan: %v", err)
	}

	created, err := store.CreateToken(ctx, CreateTokenInput{
		Name:         "insar-v2",
		BasemapCodes: []string{"google_image"},
		VectorCodes:  []string{"全国行政区.geojson", "landuse/parcel.shp"},
	})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	auth, err := store.AuthorizeToken(ctx, created.Token)
	if err != nil {
		t.Fatalf("authorize token: %v", err)
	}

	tile, err := store.ResolveDefaultTile(ctx, auth, "google_image", "0/0/0.webp")
	if err != nil {
		t.Fatalf("resolve default tile: %v", err)
	}
	if tile.Version.Version != "current" {
		t.Fatalf("unexpected default version: %s", tile.Version.Version)
	}

	geojson, err := store.ResolveVectorDownloadByRef(ctx, auth, "全国行政区.geojson", "geojson")
	if err != nil {
		t.Fatalf("resolve geojson by path: %v", err)
	}
	if geojson.Asset.Format != "geojson" {
		t.Fatalf("unexpected geojson format: %s", geojson.Asset.Format)
	}
	if filepath.Base(geojson.FilePath) != "全国行政区.geojson" {
		t.Fatalf("unexpected geojson file: %s", geojson.FilePath)
	}

	shp, err := store.ResolveVectorDownloadByRef(ctx, auth, "landuse/parcel.shp", "shapefile")
	if err != nil {
		t.Fatalf("resolve shapefile by path: %v", err)
	}
	if !shp.Zipped {
		t.Fatal("expected shapefile download to be zipped")
	}
	if shp.FileName != "parcel.zip" {
		t.Fatalf("unexpected shapefile name: %s", shp.FileName)
	}
	if len(shp.PackageFiles) != 3 {
		t.Fatalf("unexpected shapefile package size: %d", len(shp.PackageFiles))
	}
}
