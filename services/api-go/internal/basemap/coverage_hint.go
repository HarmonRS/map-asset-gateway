package basemap

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"path/filepath"
	"strconv"
	"strings"
)

const coverageHintTileSize = 256

type coverageHintPoint struct {
	X float64
	Y float64
}

// renderCoverageHintTile synthesizes a transparent PNG that points out where a
// high-min-zoom layer has data. It is only used when the requested zoom is
// below the layer's real minimum zoom; real tiles always win when present.
func renderCoverageHintTile(version BasemapVersion, tilePath string) ([]byte, bool, error) {
	z, x, y, ok := parseCoverageHintTilePath(tilePath)
	if !ok || len(version.Coverage) == 0 || z >= version.MinZoom {
		return nil, false, nil
	}
	if strings.EqualFold(strings.TrimSpace(version.TileScheme), "tms") {
		y = (1 << z) - 1 - y
	}

	tileBounds := xyzTileBounds(z, x, y)
	img := image.NewNRGBA(image.Rect(0, 0, coverageHintTileSize, coverageHintTileSize))
	var points []coverageHintPoint
	intersects := false

	for _, area := range version.Coverage {
		if !boundsOverlap(area, tileBounds) {
			continue
		}
		intersects = true

		left, top, right, bottom := coverageRectPixels(z, x, y, area)
		if right-left >= 4 && bottom-top >= 3 {
			fill := image.Rect(left, top, right, bottom)
			draw.Draw(img, fill, image.NewUniform(color.NRGBA{R: 0, G: 190, B: 255, A: 46}), image.Point{}, draw.Over)
			drawCoverageBorder(img, fill)
		}

		centerX, centerY := coverageCenterPixels(z, x, y, area)
		points = append(points, coverageHintPoint{
			X: clampFloat(centerX, 12, coverageHintTileSize-12),
			Y: clampFloat(centerY, 12, coverageHintTileSize-12),
		})
	}
	if !intersects {
		return nil, false, nil
	}

	for _, point := range clusterCoverageHintPoints(points) {
		drawCoverageMarker(img, int(math.Round(point.X)), int(math.Round(point.Y)))
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, false, fmt.Errorf("encode coverage hint png: %w", err)
	}
	return buf.Bytes(), true, nil
}

func parseCoverageHintTilePath(tilePath string) (int, int, int, bool) {
	parts := strings.Split(filepath.ToSlash(filepath.Clean(tilePath)), "/")
	if len(parts) != 3 {
		return 0, 0, 0, false
	}
	z, errZ := strconv.Atoi(parts[0])
	x, errX := strconv.Atoi(parts[1])
	name := parts[2]
	y, errY := strconv.Atoi(strings.TrimSuffix(name, filepath.Ext(name)))
	if errZ != nil || errX != nil || errY != nil || z < 0 || z > 30 {
		return 0, 0, 0, false
	}
	size := 1 << z
	if x < 0 || y < 0 || x >= size || y >= size {
		return 0, 0, 0, false
	}
	return z, x, y, true
}

func xyzTileBounds(z, x, y int) Bounds {
	n := int64(1) << z
	return Bounds{
		MinLon: tileLon(int64(x), n),
		MinLat: tileLat(int64(y+1), n),
		MaxLon: tileLon(int64(x+1), n),
		MaxLat: tileLat(int64(y), n),
	}
}

func boundsOverlap(a, b Bounds) bool {
	return a.MinLon < b.MaxLon && a.MaxLon > b.MinLon &&
		a.MinLat < b.MaxLat && a.MaxLat > b.MinLat
}

func coverageRectPixels(z, x, y int, area Bounds) (int, int, int, int) {
	originX := float64(x * coverageHintTileSize)
	originY := float64(y * coverageHintTileSize)
	left := clampFloat(projectLonPixel(area.MinLon, z)-originX, 0, coverageHintTileSize)
	right := clampFloat(projectLonPixel(area.MaxLon, z)-originX, 0, coverageHintTileSize)
	top := clampFloat(projectLatPixel(area.MaxLat, z)-originY, 0, coverageHintTileSize)
	bottom := clampFloat(projectLatPixel(area.MinLat, z)-originY, 0, coverageHintTileSize)
	return int(math.Floor(left)), int(math.Floor(top)), int(math.Ceil(right)), int(math.Ceil(bottom))
}

func coverageCenterPixels(z, x, y int, area Bounds) (float64, float64) {
	lon := (area.MinLon + area.MaxLon) / 2
	lat := (area.MinLat + area.MaxLat) / 2
	return projectLonPixel(lon, z) - float64(x*coverageHintTileSize),
		projectLatPixel(lat, z) - float64(y*coverageHintTileSize)
}

func projectLonPixel(lon float64, z int) float64 {
	worldSize := float64(coverageHintTileSize) * math.Exp2(float64(z))
	return (lon + 180) / 360 * worldSize
}

func projectLatPixel(lat float64, z int) float64 {
	lat = clampFloat(lat, -85.05112878, 85.05112878)
	rad := lat * math.Pi / 180
	worldSize := float64(coverageHintTileSize) * math.Exp2(float64(z))
	return (1 - math.Log(math.Tan(rad)+1/math.Cos(rad))/math.Pi) / 2 * worldSize
}

func clampFloat(value, minValue, maxValue float64) float64 {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func drawCoverageBorder(img *image.NRGBA, rect image.Rectangle) {
	border := image.NewUniform(color.NRGBA{R: 0, G: 220, B: 255, A: 220})
	draw.Draw(img, image.Rect(rect.Min.X, rect.Min.Y, rect.Max.X, rect.Min.Y+2), border, image.Point{}, draw.Over)
	draw.Draw(img, image.Rect(rect.Min.X, rect.Max.Y-2, rect.Max.X, rect.Max.Y), border, image.Point{}, draw.Over)
	draw.Draw(img, image.Rect(rect.Min.X, rect.Min.Y, rect.Min.X+2, rect.Max.Y), border, image.Point{}, draw.Over)
	draw.Draw(img, image.Rect(rect.Max.X-2, rect.Min.Y, rect.Max.X, rect.Max.Y), border, image.Point{}, draw.Over)
}

func clusterCoverageHintPoints(points []coverageHintPoint) []coverageHintPoint {
	const mergeDistance = 18.0
	var clustered []coverageHintPoint
	for _, point := range points {
		merged := false
		for i := range clustered {
			if math.Hypot(clustered[i].X-point.X, clustered[i].Y-point.Y) <= mergeDistance {
				clustered[i].X = (clustered[i].X + point.X) / 2
				clustered[i].Y = (clustered[i].Y + point.Y) / 2
				merged = true
				break
			}
		}
		if !merged {
			clustered = append(clustered, point)
		}
	}
	return clustered
}

func drawCoverageMarker(img *image.NRGBA, cx, cy int) {
	drawCircle(img, cx, cy, 12, color.NRGBA{R: 0, G: 0, B: 0, A: 90})
	drawCircle(img, cx, cy, 10, color.NRGBA{R: 255, G: 255, B: 255, A: 245})
	drawCircle(img, cx, cy, 8, color.NRGBA{R: 255, G: 138, B: 0, A: 255})
	drawCircle(img, cx, cy, 5, color.NRGBA{R: 255, G: 202, B: 72, A: 255})

	labelX := cx - 11
	labelY := cy + 14
	if labelY+10 >= coverageHintTileSize {
		labelY = cy - 24
	}
	drawCoverageText(img, "UAV", labelX+1, labelY+1, 2, color.NRGBA{A: 180})
	drawCoverageText(img, "UAV", labelX, labelY, 2, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
}

func drawCircle(img *image.NRGBA, cx, cy, radius int, fill color.NRGBA) {
	radiusSquared := radius * radius
	for y := cy - radius; y <= cy+radius; y++ {
		for x := cx - radius; x <= cx+radius; x++ {
			dx := x - cx
			dy := y - cy
			if dx*dx+dy*dy <= radiusSquared {
				blendPixel(img, x, y, fill)
			}
		}
	}
}

func drawCoverageText(img *image.NRGBA, text string, x, y, scale int, fill color.NRGBA) {
	cursor := x
	for i := 0; i < len(text); i++ {
		glyph, ok := coverageHintGlyph(text[i])
		if !ok {
			cursor += 4 * scale
			continue
		}
		for row := 0; row < 5; row++ {
			for col := 0; col < 3; col++ {
				if glyph[row]&(1<<uint(2-col)) == 0 {
					continue
				}
				for dy := 0; dy < scale; dy++ {
					for dx := 0; dx < scale; dx++ {
						blendPixel(img, cursor+col*scale+dx, y+row*scale+dy, fill)
					}
				}
			}
		}
		cursor += 4 * scale
	}
}

func coverageHintGlyph(ch byte) ([5]uint8, bool) {
	switch ch {
	case 'U':
		return [5]uint8{0b101, 0b101, 0b101, 0b101, 0b010}, true
	case 'A':
		return [5]uint8{0b010, 0b101, 0b111, 0b101, 0b101}, true
	case 'V':
		return [5]uint8{0b101, 0b101, 0b101, 0b010, 0b010}, true
	default:
		return [5]uint8{}, false
	}
}

func blendPixel(img *image.NRGBA, x, y int, src color.NRGBA) {
	if !image.Pt(x, y).In(img.Bounds()) {
		return
	}
	dst := img.NRGBAAt(x, y)
	sa := float64(src.A) / 255
	da := float64(dst.A) / 255
	outAlpha := sa + da*(1-sa)
	if outAlpha == 0 {
		return
	}
	outR := (float64(src.R)*sa + float64(dst.R)*da*(1-sa)) / outAlpha
	outG := (float64(src.G)*sa + float64(dst.G)*da*(1-sa)) / outAlpha
	outB := (float64(src.B)*sa + float64(dst.B)*da*(1-sa)) / outAlpha
	img.SetNRGBA(x, y, color.NRGBA{
		R: uint8(math.Round(outR)),
		G: uint8(math.Round(outG)),
		B: uint8(math.Round(outB)),
		A: uint8(math.Round(outAlpha * 255)),
	})
}
