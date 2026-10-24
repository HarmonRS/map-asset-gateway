package basemap

import (
	"bytes"
	"fmt"
	"image/png"
	"testing"
)

func TestRenderCoverageHintTile(t *testing.T) {
	version := BasemapVersion{
		MinZoom:    12,
		MaxZoom:    22,
		TileScheme: "xyz",
		Coverage: []Bounds{{
			MinLon: 124.60,
			MinLat: 52.90,
			MaxLon: 124.80,
			MaxLat: 53.10,
		}},
	}

	z := 5
	x := int(projectLonPixel(124.70, z) / coverageHintTileSize)
	y := int(projectLatPixel(53.00, z) / coverageHintTileSize)
	tilePath := fmt.Sprintf("%d/%d/%d.png", z, x, y)

	data, ok, err := renderCoverageHintTile(version, tilePath)
	if err != nil {
		t.Fatalf("render hint: %v", err)
	}
	if !ok {
		t.Fatal("expected a hint tile inside the coverage area")
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode hint png: %v", err)
	}
	if img.Bounds().Dx() != coverageHintTileSize || img.Bounds().Dy() != coverageHintTileSize {
		t.Fatalf("unexpected hint dimensions: %v", img.Bounds())
	}
	opaquePixels := 0
	for py := img.Bounds().Min.Y; py < img.Bounds().Max.Y; py++ {
		for px := img.Bounds().Min.X; px < img.Bounds().Max.X; px++ {
			_, _, _, alpha := img.At(px, py).RGBA()
			if alpha > 0 {
				opaquePixels++
			}
		}
	}
	if opaquePixels < 100 {
		t.Fatalf("expected a visible coverage marker, got %d painted pixels", opaquePixels)
	}

	outsideX := int(projectLonPixel(0, z) / coverageHintTileSize)
	outsideY := int(projectLatPixel(0, z) / coverageHintTileSize)
	if _, ok, err := renderCoverageHintTile(version, fmt.Sprintf("%d/%d/%d.png", z, outsideX, outsideY)); err != nil || ok {
		t.Fatalf("expected no hint outside coverage, ok=%v err=%v", ok, err)
	}

	if _, ok, err := renderCoverageHintTile(version, fmt.Sprintf("12/%d/%d.png", 1<<10, 1<<10)); err != nil || ok {
		t.Fatalf("expected no hint at the real minimum zoom, ok=%v err=%v", ok, err)
	}
}

func TestParseCoverageHintTilePath(t *testing.T) {
	z, x, y, ok := parseCoverageHintTilePath("5/10/12.png")
	if !ok || z != 5 || x != 10 || y != 12 {
		t.Fatalf("unexpected parse result: z=%d x=%d y=%d ok=%v", z, x, y, ok)
	}
	for _, value := range []string{"", "5/10", "5/10/12/13.png", "5/a/12.png", "5/32/0.png", "5/-1/0.png"} {
		if _, _, _, ok := parseCoverageHintTilePath(value); ok {
			t.Fatalf("expected invalid tile path %q", value)
		}
	}
}
