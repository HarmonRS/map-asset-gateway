package basemap

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// deriveCoverage reads only the lowest zoom level of a tile tree. At that zoom
// a project usually has a handful of x/y directories, so this stays cheap even
// when the full tree contains hundreds of thousands of files.
//
// Adjacent tiles are merged into rectangles. Disjoint captures remain separate
// rectangles, which is important for mosaics made of scattered flights: one
// overall bbox would include the empty space between them.
func deriveCoverage(tileRoot string, minZoom int, tileScheme string) ([]Bounds, int, error) {
	if minZoom < 0 || minZoom > 30 {
		return nil, 0, nil
	}
	zoomPath := filepath.Join(tileRoot, strconv.Itoa(minZoom))
	entries, err := os.ReadDir(zoomPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, 0, nil
		}
		return nil, 0, fmt.Errorf("read coverage zoom %s: %w", zoomPath, err)
	}

	n := int64(1) << minZoom
	scheme := strings.ToLower(strings.TrimSpace(tileScheme))
	if scheme == "" {
		scheme = "xyz"
	}

	spansByX := make(map[int64][]tileSpan)
	for _, xEntry := range entries {
		if !xEntry.IsDir() {
			continue
		}
		x, err := strconv.ParseInt(xEntry.Name(), 10, 64)
		if err != nil || x < 0 || x >= n {
			continue
		}
		yEntries, err := os.ReadDir(filepath.Join(zoomPath, xEntry.Name()))
		if err != nil {
			return nil, 0, fmt.Errorf("read coverage x %s: %w", xEntry.Name(), err)
		}
		var ys []int64
		for _, yEntry := range yEntries {
			if yEntry.IsDir() {
				continue
			}
			y, ok := tileYFromName(yEntry.Name())
			if !ok || y < 0 || y >= n {
				continue
			}
			if scheme == "tms" {
				y = n - 1 - y
			}
			ys = append(ys, y)
		}
		if len(ys) == 0 {
			continue
		}
		sort.Slice(ys, func(i, j int) bool { return ys[i] < ys[j] })
		spansByX[x] = mergeYTiles(ys)
	}
	if len(spansByX) == 0 {
		return nil, 0, nil
	}

	xs := make([]int64, 0, len(spansByX))
	for x := range spansByX {
		xs = append(xs, x)
	}
	sort.Slice(xs, func(i, j int) bool { return xs[i] < xs[j] })

	type activeRect struct {
		xStart int64
		xEnd   int64
		yStart int64
		yEnd   int64
	}
	active := make(map[tileSpan]*activeRect)
	var rects []*activeRect
	for _, x := range xs {
		next := make(map[tileSpan]*activeRect)
		for _, span := range spansByX[x] {
			if rect, ok := active[span]; ok && rect.xEnd+1 == x {
				rect.xEnd = x
				next[span] = rect
				continue
			}
			rect := &activeRect{xStart: x, xEnd: x, yStart: span.YStart, yEnd: span.YEnd}
			rects = append(rects, rect)
			next[span] = rect
		}
		active = next
	}

	coverage := make([]Bounds, 0, len(rects))
	for _, rect := range rects {
		coverage = append(coverage, Bounds{
			MinLon: tileLon(rect.xStart, n),
			MinLat: tileLat(rect.yEnd+1, n),
			MaxLon: tileLon(rect.xEnd+1, n),
			MaxLat: tileLat(rect.yStart, n),
		})
	}
	sort.Slice(coverage, func(i, j int) bool {
		if coverage[i].MinLon != coverage[j].MinLon {
			return coverage[i].MinLon < coverage[j].MinLon
		}
		if coverage[i].MinLat != coverage[j].MinLat {
			return coverage[i].MinLat < coverage[j].MinLat
		}
		if coverage[i].MaxLon != coverage[j].MaxLon {
			return coverage[i].MaxLon < coverage[j].MaxLon
		}
		return coverage[i].MaxLat < coverage[j].MaxLat
	})
	return coverage, minZoom, nil
}

type tileSpan struct {
	YStart int64
	YEnd   int64
}

func mergeYTiles(ys []int64) []tileSpan {
	if len(ys) == 0 {
		return nil
	}
	spans := make([]tileSpan, 0, len(ys))
	start, end := ys[0], ys[0]
	for _, y := range ys[1:] {
		if y == end || y == end+1 {
			end = y
			continue
		}
		spans = append(spans, tileSpan{YStart: start, YEnd: end})
		start, end = y, y
	}
	spans = append(spans, tileSpan{YStart: start, YEnd: end})
	return spans
}

func tileYFromName(name string) (int64, bool) {
	base := strings.TrimSuffix(name, filepath.Ext(name))
	if base == "" {
		return 0, false
	}
	y, err := strconv.ParseInt(base, 10, 64)
	return y, err == nil
}

func tileLon(x, n int64) float64 {
	return float64(x)/float64(n)*360.0 - 180.0
}

func tileLat(y, n int64) float64 {
	return math.Atan(math.Sinh(math.Pi*(1.0-2.0*float64(y)/float64(n)))) * 180.0 / math.Pi
}
