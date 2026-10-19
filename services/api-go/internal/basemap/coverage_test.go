package basemap

import (
	"context"
	"math"
	"path/filepath"
	"testing"
)

func TestDeriveCoverageMergesTilesAndKeepsDisjointAreas(t *testing.T) {
	root := t.TempDir()
	for _, tile := range []string{
		"2/0/1.png",
		"2/1/1.png",
		"2/3/2.png",
	} {
		writeTestFile(t, filepath.Join(root, filepath.FromSlash(tile)), "tile")
	}

	coverage, zoom, err := deriveCoverage(root, 2, "xyz")
	if err != nil {
		t.Fatalf("derive coverage: %v", err)
	}
	if zoom != 2 {
		t.Fatalf("expected coverage zoom 2, got %d", zoom)
	}
	if len(coverage) != 2 {
		t.Fatalf("expected two disjoint coverage areas, got %+v", coverage)
	}

	assertNearlyEqual(t, "first min lon", coverage[0].MinLon, -180)
	assertNearlyEqual(t, "first max lon", coverage[0].MaxLon, 0)
	assertNearlyEqual(t, "first min lat", coverage[0].MinLat, 0)
	assertNearlyEqual(t, "first max lat", coverage[0].MaxLat, 66.51326044311186)
	assertNearlyEqual(t, "second min lon", coverage[1].MinLon, 90)
	assertNearlyEqual(t, "second max lon", coverage[1].MaxLon, 180)
	assertNearlyEqual(t, "second min lat", coverage[1].MinLat, -66.51326044311186)
	assertNearlyEqual(t, "second max lat", coverage[1].MaxLat, 0)
}

func TestScanPersistsCoverage(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	tileRoot := filepath.Join(root, "tiles")
	writeTestFile(t, filepath.Join(tileRoot, "uav", "2", "0", "1.png"), "tile")
	writeTestFile(t, filepath.Join(tileRoot, "uav", "2", "1", "1.png"), "tile")
	writeTestFile(t, filepath.Join(tileRoot, "uav", "2", "3", "2.png"), "tile")

	store := openTestStore(t, StoreConfig{
		SQLitePath:    filepath.Join(root, "basemap.db"),
		ScanRoot:      tileRoot,
		APIBaseURL:    "http://127.0.0.1:8910",
		TileBaseURL:   "http://127.0.0.1:8910",
		VectorBaseURL: "http://127.0.0.1:8910",
	})
	if _, err := store.RunScan(ctx, defaultScanSourceCode); err != nil {
		t.Fatalf("scan: %v", err)
	}

	basemaps, err := store.ListBasemaps(ctx)
	if err != nil {
		t.Fatalf("list basemaps: %v", err)
	}
	if len(basemaps) != 1 || basemaps[0].Default == nil {
		t.Fatalf("expected one basemap with a default version, got %+v", basemaps)
	}
	version := basemaps[0].Default
	if version.CoverageZoom != 2 {
		t.Fatalf("expected persisted coverage zoom 2, got %d", version.CoverageZoom)
	}
	if len(version.Coverage) != 2 {
		t.Fatalf("expected persisted disjoint coverage, got %+v", version.Coverage)
	}
}

func assertNearlyEqual(t *testing.T, label string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("%s: got %.12f, want %.12f", label, got, want)
	}
}
