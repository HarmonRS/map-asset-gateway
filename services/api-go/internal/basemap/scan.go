package basemap

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"tile-server/api-go/internal/uid"
)

type scannedVersion struct {
	BasemapCode string
	Name        string
	Type        string
	Status      string
	Description string
	Version     string
	Manifest    string
	TileRoot    string
	TileFormat  string
	TileScheme  string
	MinZoom     int
	MaxZoom     int
	BBox        []float64
	Attribution string
	Metadata    map[string]any
	IsDefault   bool
}

// scanCounts reports what a scan actually changed, so operators can tell a real
// update from a no-op rescan.
type scanCounts struct {
	Scanned   int
	Added     int
	Updated   int
	Removed   int
	Unchanged int
}

func (s *Store) RunScan(ctx context.Context, sourceCode string) (ScanRun, error) {
	// A scan reads the current catalog and then rewrites it, so two overlapping
	// runs could delete each other's rows. Refuse instead of interleaving.
	if !s.scanMu.TryLock() {
		return ScanRun{}, ErrScanInProgress
	}
	defer s.scanMu.Unlock()

	source, err := s.GetScanSourceByCode(ctx, sourceCode)
	if err != nil {
		return ScanRun{}, err
	}
	if !source.Enabled {
		return ScanRun{}, &scanSourceError{sentinel: ErrScanSourceDisabled, code: sourceCode, reason: "is disabled"}
	}

	startedAt := nowUTC()
	runID := uid.New()
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO scan_runs (
			id, scan_source_id, status, scanned_count, added_count, updated_count, removed_count, summary_json, started_at, finished_at
		) VALUES (?, ?, 'running', 0, 0, 0, 0, '', ?, ?)
	`, runID, source.ID, toRFC3339(startedAt), toRFC3339(startedAt)); err != nil {
		return ScanRun{}, fmt.Errorf("create scan run: %w", err)
	}

	run, finalErr := s.runScan(ctx, runID, source, startedAt)
	return run, finalErr
}

func (s *Store) runScan(ctx context.Context, runID string, source ScanSource, startedAt time.Time) (ScanRun, error) {
	fail := func(err error) (ScanRun, error) {
		return s.finishScanRun(ctx, runID, source, startedAt, "failed", nil, scanCounts{}, err)
	}

	rootInfo, err := os.Stat(source.RootPath)
	if err != nil {
		return fail(fmt.Errorf("stat scan root: %w", err))
	}
	if !rootInfo.IsDir() {
		return fail(fmt.Errorf("scan root %s is not a directory", source.RootPath))
	}

	manifestItems, coveredRoots, err := s.findManifestVersions(source)
	if err != nil {
		return fail(err)
	}
	fallbackItems, err := s.findDirectTileRoots(source, coveredRoots)
	if err != nil {
		return fail(err)
	}

	allItems := append(manifestItems, fallbackItems...)
	sort.Slice(allItems, func(i, j int) bool {
		if allItems[i].BasemapCode == allItems[j].BasemapCode {
			return allItems[i].Version < allItems[j].Version
		}
		return allItems[i].BasemapCode < allItems[j].BasemapCode
	})

	counts, err := s.persistScanResult(ctx, source, allItems)
	if err != nil {
		return s.finishScanRun(ctx, runID, source, startedAt, "failed", allItems, scanCounts{Scanned: len(allItems)}, err)
	}
	counts.Scanned = len(allItems)
	return s.finishScanRun(ctx, runID, source, startedAt, "succeeded", allItems, counts, nil)
}

func (s *Store) finishScanRun(
	ctx context.Context,
	runID string,
	source ScanSource,
	startedAt time.Time,
	status string,
	items []scannedVersion,
	counts scanCounts,
	runErr error,
) (ScanRun, error) {
	finishedAt := nowUTC()
	summary := map[string]any{
		"source_code":     source.Code,
		"scan_root":       source.RootPath,
		"scanned_count":   counts.Scanned,
		"added_count":     counts.Added,
		"updated_count":   counts.Updated,
		"removed_count":   counts.Removed,
		"unchanged_count": counts.Unchanged,
	}
	if runErr != nil {
		summary["error"] = runErr.Error()
	}
	if len(items) > 0 {
		codes := map[string]struct{}{}
		for _, item := range items {
			codes[item.BasemapCode] = struct{}{}
		}
		summary["basemap_codes"] = sortedKeys(codes)
	}

	_, updateErr := s.db.ExecContext(ctx, `
		UPDATE scan_runs
		SET status = ?, scanned_count = ?, added_count = ?, updated_count = ?, removed_count = ?, summary_json = ?, finished_at = ?
		WHERE id = ?
	`, status, counts.Scanned, counts.Added, counts.Updated, counts.Removed, writeJSON(summary), toRFC3339(finishedAt), runID)
	if updateErr != nil {
		if runErr != nil {
			return ScanRun{}, fmt.Errorf("%v; update scan run: %w", runErr, updateErr)
		}
		return ScanRun{}, updateErr
	}

	run := ScanRun{
		ID:           runID,
		ScanSourceID: source.ID,
		SourceCode:   source.Code,
		Status:       status,
		ScannedCount: counts.Scanned,
		AddedCount:   counts.Added,
		UpdatedCount: counts.Updated,
		RemovedCount: counts.Removed,
		Summary:      summary,
		StartedAt:    startedAt,
		FinishedAt:   finishedAt,
	}
	if runErr != nil {
		return run, runErr
	}
	return run, nil
}

// manifestScanMaxDepth bounds how deep the manifest lookup descends. Manifest
// based basemaps place manifest.json at the basemap root or one version folder
// below it, so anything deeper is tile payload that must not be walked.
const manifestScanMaxDepth = 4

func (s *Store) findManifestVersions(source ScanSource) ([]scannedVersion, map[string]struct{}, error) {
	manifestName := strings.TrimSpace(source.ManifestName)
	if manifestName == "" {
		manifestName = defaultManifestName
	}

	var items []scannedVersion
	coveredRoots := map[string]struct{}{}

	var walk func(dir string, depth int) error
	walk = func(dir string, depth int) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}

		// A directory holding a manifest is a basemap root: record it and stop,
		// so the tile payload underneath (the default "tiles" subtree) is never
		// traversed.
		for _, entry := range entries {
			if entry.IsDir() || !strings.EqualFold(entry.Name(), manifestName) {
				continue
			}
			manifestPath := filepath.Join(dir, entry.Name())
			manifest, err := LoadManifest(manifestPath)
			if err != nil {
				return err
			}
			tileRoot, err := resolveTileRoot(dir, manifest.RootPath)
			if err != nil {
				return err
			}
			format, minZoom, maxZoom, found, err := inspectTileRoot(tileRoot, manifest.TileFormat)
			if err != nil {
				return err
			}
			if found {
				manifest.TileFormat = format
				manifest.MinZoom = minZoom
				manifest.MaxZoom = maxZoom
			}

			if relPath, err := filepath.Rel(source.RootPath, tileRoot); err == nil {
				first := strings.Split(filepath.ToSlash(relPath), "/")[0]
				if first != "" && first != "." {
					coveredRoots[first] = struct{}{}
				}
			}

			items = append(items, scannedVersion{
				BasemapCode: manifest.Code,
				Name:        defaultText(manifest.Name, manifest.Code),
				Type:        defaultText(manifest.Type, "xyz"),
				Status:      defaultText(manifest.Status, "ready"),
				Description: manifest.Description,
				Version:     defaultText(manifest.Version, "current"),
				Manifest:    manifestPath,
				TileRoot:    tileRoot,
				TileFormat:  normalizeTileFormat(manifest.TileFormat),
				TileScheme:  defaultText(manifest.TileScheme, "xyz"),
				MinZoom:     manifest.MinZoom,
				MaxZoom:     manifest.MaxZoom,
				BBox:        manifest.BBox,
				Attribution: manifest.Attribution,
				Metadata:    manifest.Metadata,
				IsDefault:   manifest.IsDefault,
			})
			return nil
		}

		if depth >= manifestScanMaxDepth {
			return nil
		}
		for _, entry := range entries {
			if !entry.IsDir() || isNumericDirName(entry.Name()) {
				continue
			}
			if err := walk(filepath.Join(dir, entry.Name()), depth+1); err != nil {
				return err
			}
		}
		return nil
	}

	if err := walk(source.RootPath, 0); err != nil {
		return nil, nil, fmt.Errorf("walk manifests: %w", err)
	}
	return items, coveredRoots, nil
}

// isNumericDirName reports whether the directory name is a bare zoom level such
// as "0" or "14". Raw tile roots nest z/x/y under the basemap directory, so
// pruning these keeps the scan clear of the tile payload.
func isNumericDirName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func (s *Store) findDirectTileRoots(source ScanSource, skip map[string]struct{}) ([]scannedVersion, error) {
	entries, err := os.ReadDir(source.RootPath)
	if err != nil {
		return nil, fmt.Errorf("read scan root: %w", err)
	}

	var items []scannedVersion
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if _, exists := skip[name]; exists {
			continue
		}

		rootPath := filepath.Join(source.RootPath, name)
		format, minZoom, maxZoom, found, err := inspectTileRoot(rootPath, "")
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}

		code := normalizeBasemapCode(name)
		if code == "" {
			continue
		}
		items = append(items, scannedVersion{
			BasemapCode: code,
			Name:        name,
			Type:        "xyz",
			Status:      "ready",
			Version:     "current",
			TileRoot:    rootPath,
			TileFormat:  format,
			TileScheme:  "xyz",
			MinZoom:     minZoom,
			MaxZoom:     maxZoom,
			Metadata: map[string]any{
				"discovered_from": "directory_scan",
			},
			IsDefault: true,
		})
	}
	return items, nil
}

func inspectTileRoot(rootPath string, hintFormat string) (string, int, int, bool, error) {
	entries, err := os.ReadDir(rootPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", 0, 0, false, nil
		}
		return "", 0, 0, false, fmt.Errorf("read tile root %s: %w", rootPath, err)
	}

	var zooms []int
	format := normalizeTileFormat(hintFormat)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		zoom, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		zooms = append(zooms, zoom)
		if format == "" || format == "png" {
			detected, found, err := detectTileFormat(filepath.Join(rootPath, entry.Name()))
			if err != nil {
				return "", 0, 0, false, err
			}
			if found {
				format = detected
			}
		}
	}
	if len(zooms) == 0 {
		return "", 0, 0, false, nil
	}
	sort.Ints(zooms)
	if format == "" {
		format = "png"
	}
	return format, zooms[0], zooms[len(zooms)-1], true, nil
}

func detectTileFormat(zoomPath string) (string, bool, error) {
	xEntries, err := os.ReadDir(zoomPath)
	if err != nil {
		return "", false, fmt.Errorf("read zoom path %s: %w", zoomPath, err)
	}
	for _, xEntry := range xEntries {
		if !xEntry.IsDir() {
			continue
		}
		tileEntries, err := os.ReadDir(filepath.Join(zoomPath, xEntry.Name()))
		if err != nil {
			return "", false, err
		}
		for _, tileEntry := range tileEntries {
			if tileEntry.IsDir() {
				continue
			}
			ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(tileEntry.Name())), ".")
			if ext == "" {
				continue
			}
			return normalizeTileFormat(ext), true, nil
		}
	}
	return "", false, nil
}

func resolveTileRoot(baseDir, relativePath string) (string, error) {
	if strings.TrimSpace(relativePath) == "" {
		relativePath = "tiles"
	}
	if filepath.IsAbs(relativePath) {
		return filepath.Clean(relativePath), nil
	}
	resolved := filepath.Clean(filepath.Join(baseDir, relativePath))
	return resolved, nil
}

// existingVersion is a snapshot of a stored version, used to detect whether a
// rescan actually changed anything.
type existingVersion struct {
	Name         string
	Type         string
	Status       string
	Description  string
	ManifestPath string
	TileRootPath string
	TileFormat   string
	TileScheme   string
	MinZoom      int
	MaxZoom      int
	IsDefault    bool
	BBoxJSON     string
	Attribution  string
	MetadataJSON string
}

func (s *Store) persistScanResult(ctx context.Context, source ScanSource, items []scannedVersion) (scanCounts, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return scanCounts{}, err
	}
	defer tx.Rollback()

	existing := map[string]existingVersion{}
	rows, err := tx.QueryContext(ctx, `
		SELECT
			b.code,
			v.version,
			b.name,
			b.type,
			b.status,
			b.description,
			v.manifest_path,
			v.tile_root_path,
			v.tile_format,
			v.tile_scheme,
			v.min_zoom,
			v.max_zoom,
			v.is_default,
			v.bbox_json,
			v.attribution,
			v.metadata_json
		FROM basemap_versions v
		JOIN basemaps b ON b.id = v.basemap_id
		WHERE v.scan_source_id = ?
	`, source.ID)
	if err != nil {
		return scanCounts{}, err
	}
	for rows.Next() {
		var code string
		var version string
		var item existingVersion
		var isDefault int
		if err := rows.Scan(
			&code,
			&version,
			&item.Name,
			&item.Type,
			&item.Status,
			&item.Description,
			&item.ManifestPath,
			&item.TileRootPath,
			&item.TileFormat,
			&item.TileScheme,
			&item.MinZoom,
			&item.MaxZoom,
			&isDefault,
			&item.BBoxJSON,
			&item.Attribution,
			&item.MetadataJSON,
		); err != nil {
			rows.Close()
			return scanCounts{}, err
		}
		item.IsDefault = isDefault == 1
		existing[code+"::"+version] = item
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return scanCounts{}, err
	}

	now := nowUTC()
	found := map[string]struct{}{}
	touchedBasemaps := map[string]struct{}{}
	counts := scanCounts{}
	for _, item := range items {
		key := item.BasemapCode + "::" + item.Version
		found[key] = struct{}{}
		if prev, exists := existing[key]; exists {
			if scannedVersionMatches(item, prev) {
				counts.Unchanged++
			} else {
				counts.Updated++
			}
		} else {
			counts.Added++
		}
		if err := s.upsertScannedVersionTx(ctx, tx, source, item, now); err != nil {
			return scanCounts{}, err
		}
		touchedBasemaps[item.BasemapCode] = struct{}{}
	}

	for key := range existing {
		if _, ok := found[key]; ok {
			continue
		}
		parts := strings.SplitN(key, "::", 2)
		if len(parts) != 2 {
			continue
		}
		if err := s.deleteVersionBySourceTx(ctx, tx, source.ID, parts[0], parts[1]); err != nil {
			return scanCounts{}, err
		}
		touchedBasemaps[parts[0]] = struct{}{}
		counts.Removed++
	}

	for code := range touchedBasemaps {
		if err := s.normalizeBasemapDefaultsTx(ctx, tx, code, now); err != nil {
			return scanCounts{}, err
		}
	}

	if err := tx.Commit(); err != nil {
		return scanCounts{}, err
	}
	return counts, nil
}

// scannedVersionMatches reports whether a freshly scanned version is identical
// to what is already stored, so an unchanged rescan is not reported as updated.
func scannedVersionMatches(item scannedVersion, prev existingVersion) bool {
	return item.Name == prev.Name &&
		item.Type == prev.Type &&
		item.Status == prev.Status &&
		item.Description == prev.Description &&
		item.Manifest == prev.ManifestPath &&
		item.TileRoot == prev.TileRootPath &&
		normalizeTileFormat(item.TileFormat) == prev.TileFormat &&
		item.TileScheme == prev.TileScheme &&
		item.MinZoom == prev.MinZoom &&
		item.MaxZoom == prev.MaxZoom &&
		item.IsDefault == prev.IsDefault &&
		writeJSON(item.BBox) == prev.BBoxJSON &&
		item.Attribution == prev.Attribution &&
		writeJSON(item.Metadata) == prev.MetadataJSON
}

func (s *Store) upsertScannedVersionTx(ctx context.Context, tx *sql.Tx, source ScanSource, item scannedVersion, now time.Time) error {
	basemapID := uid.Deterministic("basemap", item.BasemapCode)
	versionID := uid.Deterministic("basemap-version", item.BasemapCode, item.Version)

	_, err := tx.ExecContext(ctx, `
		INSERT INTO basemaps (id, code, name, type, status, description, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(code) DO UPDATE SET
			name = excluded.name,
			type = excluded.type,
			status = excluded.status,
			description = excluded.description,
			updated_at = excluded.updated_at
	`, basemapID, item.BasemapCode, item.Name, item.Type, item.Status, item.Description, toRFC3339(now), toRFC3339(now))
	if err != nil {
		return err
	}

	isDefault := 0
	if item.IsDefault {
		isDefault = 1
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO basemap_versions (
			id, basemap_id, scan_source_id, version, status, is_default, manifest_path, tile_root_path, url_template,
			tile_format, tile_scheme, min_zoom, max_zoom, bbox_json, attribution, metadata_json, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(basemap_id, version) DO UPDATE SET
			scan_source_id = excluded.scan_source_id,
			status = excluded.status,
			is_default = CASE WHEN excluded.is_default = 1 THEN 1 ELSE basemap_versions.is_default END,
			manifest_path = excluded.manifest_path,
			tile_root_path = excluded.tile_root_path,
			url_template = excluded.url_template,
			tile_format = excluded.tile_format,
			tile_scheme = excluded.tile_scheme,
			min_zoom = excluded.min_zoom,
			max_zoom = excluded.max_zoom,
			bbox_json = excluded.bbox_json,
			attribution = excluded.attribution,
			metadata_json = excluded.metadata_json,
			updated_at = excluded.updated_at
	`, versionID, basemapID, source.ID, item.Version, item.Status, isDefault, item.Manifest, item.TileRoot, s.buildURLTemplate(item.BasemapCode, item.Version, item.TileFormat), item.TileFormat, item.TileScheme, item.MinZoom, item.MaxZoom, writeJSON(item.BBox), item.Attribution, writeJSON(item.Metadata), toRFC3339(now), toRFC3339(now))
	if err != nil {
		return err
	}
	if item.IsDefault {
		_, err = tx.ExecContext(ctx, `
			UPDATE basemap_versions
			SET is_default = CASE WHEN version = ? THEN 1 ELSE 0 END, updated_at = ?
			WHERE basemap_id = ?
		`, item.Version, toRFC3339(now), basemapID)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) deleteVersionBySourceTx(ctx context.Context, tx *sql.Tx, sourceID, basemapCode, version string) error {
	result, err := tx.ExecContext(ctx, `
		DELETE FROM basemap_versions
		WHERE scan_source_id = ? AND version = ? AND basemap_id = (SELECT id FROM basemaps WHERE code = ?)
	`, sourceID, version, basemapCode)
	if err != nil {
		return err
	}
	if _, err := result.RowsAffected(); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		DELETE FROM basemaps
		WHERE code = ? AND NOT EXISTS (
			SELECT 1 FROM basemap_versions WHERE basemap_id = basemaps.id
		)
	`, basemapCode)
	return err
}

func (s *Store) normalizeBasemapDefaultsTx(ctx context.Context, tx *sql.Tx, basemapCode string, now time.Time) error {
	var basemapID string
	err := tx.QueryRowContext(ctx, `SELECT id FROM basemaps WHERE code = ?`, basemapCode).Scan(&basemapID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT version, is_default
		FROM basemap_versions
		WHERE basemap_id = ?
		ORDER BY is_default DESC, version ASC
	`, basemapID)
	if err != nil {
		return err
	}
	defer rows.Close()

	type versionFlag struct {
		Version   string
		IsDefault bool
	}
	var versions []versionFlag
	for rows.Next() {
		var item versionFlag
		var isDefault int
		if err := rows.Scan(&item.Version, &isDefault); err != nil {
			return err
		}
		item.IsDefault = isDefault == 1
		versions = append(versions, item)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(versions) == 0 {
		return nil
	}
	defaultVersion := versions[0].Version
	for _, item := range versions {
		if item.IsDefault {
			defaultVersion = item.Version
			break
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE basemap_versions
		SET is_default = CASE WHEN version = ? THEN 1 ELSE 0 END, updated_at = ?
		WHERE basemap_id = ?
	`, defaultVersion, toRFC3339(now), basemapID); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE basemaps SET updated_at = ? WHERE id = ?`, toRFC3339(now), basemapID)
	return err
}

func (s *Store) buildURLTemplate(code, version, format string) string {
	base := trimURL(s.cfg.TileBaseURL)
	if base == "" {
		base = trimURL(s.cfg.APIBaseURL)
	}
	format = normalizeTileFormat(format)
	return fmt.Sprintf("%s/tiles/%s/%s/{z}/{x}/{y}.%s", base, code, version, format)
}
