package basemap

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"tile-server/api-go/internal/uid"
)

func (s *Store) EnsureScanSource(ctx context.Context, input CreateScanSourceInput) (ScanSource, error) {
	code := normalizeBasemapCode(input.Code)
	if code == "" {
		return ScanSource{}, errors.New("scan source code is required")
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = code
	}
	rootPath := strings.TrimSpace(input.RootPath)
	if rootPath == "" {
		return ScanSource{}, errors.New("scan source root path is required")
	}
	absRoot, err := filepath.Abs(rootPath)
	if err != nil {
		return ScanSource{}, fmt.Errorf("resolve root path: %w", err)
	}
	manifestName := strings.TrimSpace(input.ManifestName)
	if manifestName == "" {
		manifestName = defaultManifestName
	}

	now := nowUTC()
	id := uid.Deterministic("scan-source", code)
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO scan_sources (
			id, code, name, root_path, manifest_name, enabled, metadata_json, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, 1, '', ?, ?)
		ON CONFLICT(code) DO UPDATE SET
			name = excluded.name,
			root_path = excluded.root_path,
			manifest_name = excluded.manifest_name,
			enabled = 1,
			updated_at = excluded.updated_at
	`, id, code, name, absRoot, manifestName, toRFC3339(now), toRFC3339(now))
	if err != nil {
		return ScanSource{}, fmt.Errorf("upsert scan source: %w", err)
	}
	return s.GetScanSourceByCode(ctx, code)
}

func (s *Store) GetScanSourceByCode(ctx context.Context, code string) (ScanSource, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, code, name, root_path, manifest_name, enabled, metadata_json, created_at, updated_at
		FROM scan_sources
		WHERE code = ?
	`, normalizeBasemapCode(code))
	item, err := scanScanSource(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ScanSource{}, &scanSourceError{sentinel: ErrScanSourceNotFound, code: code, reason: "not found"}
		}
		return ScanSource{}, err
	}
	return item, nil
}

func (s *Store) ListScanSources(ctx context.Context) ([]ScanSource, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, code, name, root_path, manifest_name, enabled, metadata_json, created_at, updated_at
		FROM scan_sources
		ORDER BY code
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []ScanSource
	for rows.Next() {
		item, err := scanScanSource(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) ListBasemaps(ctx context.Context) ([]Basemap, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, code, name, type, status, description, created_at, updated_at
		FROM basemaps
		ORDER BY code
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []Basemap
	indexByID := map[string]int{}
	for rows.Next() {
		item, err := scanBasemap(rows)
		if err != nil {
			return nil, err
		}
		indexByID[item.ID] = len(items)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	versionRows, err := s.db.QueryContext(ctx, `
		SELECT
			v.id,
			v.basemap_id,
			b.code,
			v.version,
			v.status,
			v.is_default,
			v.manifest_path,
			v.tile_root_path,
			v.url_template,
			v.tile_format,
			v.tile_scheme,
			v.min_zoom,
			v.max_zoom,
			v.bbox_json,
			v.attribution,
			v.metadata_json,
			v.created_at,
			v.updated_at
		FROM basemap_versions v
		JOIN basemaps b ON b.id = v.basemap_id
		ORDER BY b.code, v.version
	`)
	if err != nil {
		return nil, err
	}
	defer versionRows.Close()

	for versionRows.Next() {
		row, err := scanBasemapVersionRow(versionRows)
		if err != nil {
			return nil, err
		}
		index, ok := indexByID[row.BasemapID]
		if !ok {
			continue
		}
		version := decodeBasemapVersion(row)
		items[index].Versions = append(items[index].Versions, version)
		if version.IsDefault {
			copyValue := version
			items[index].Default = &copyValue
		}
	}
	if err := versionRows.Err(); err != nil {
		return nil, err
	}

	return items, nil
}

func (s *Store) GetBasemapByCode(ctx context.Context, code string) (Basemap, error) {
	normalized := normalizeBasemapCode(code)
	row := s.db.QueryRowContext(ctx, `
		SELECT id, code, name, type, status, description, created_at, updated_at
		FROM basemaps
		WHERE code = ?
	`, normalized)
	item, err := scanBasemap(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Basemap{}, fmt.Errorf("basemap %q not found", code)
		}
		return Basemap{}, err
	}

	versionRows, err := s.db.QueryContext(ctx, `
		SELECT
			v.id,
			v.basemap_id,
			b.code,
			v.version,
			v.status,
			v.is_default,
			v.manifest_path,
			v.tile_root_path,
			v.url_template,
			v.tile_format,
			v.tile_scheme,
			v.min_zoom,
			v.max_zoom,
			v.bbox_json,
			v.attribution,
			v.metadata_json,
			v.created_at,
			v.updated_at
		FROM basemap_versions v
		JOIN basemaps b ON b.id = v.basemap_id
		WHERE v.basemap_id = ?
		ORDER BY v.version
	`, item.ID)
	if err != nil {
		return Basemap{}, err
	}
	defer versionRows.Close()

	for versionRows.Next() {
		row, err := scanBasemapVersionRow(versionRows)
		if err != nil {
			return Basemap{}, err
		}
		version := decodeBasemapVersion(row)
		item.Versions = append(item.Versions, version)
		if version.IsDefault {
			copyValue := version
			item.Default = &copyValue
		}
	}
	if err := versionRows.Err(); err != nil {
		return Basemap{}, err
	}
	return item, nil
}

func (s *Store) SetDefaultVersion(ctx context.Context, basemapCode, version string) error {
	basemapCode = normalizeBasemapCode(basemapCode)
	version = strings.TrimSpace(version)
	if basemapCode == "" || version == "" {
		return errors.New("basemap code and version are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var basemapID string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM basemaps WHERE code = ?`, basemapCode).Scan(&basemapID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("basemap %q not found", basemapCode)
		}
		return err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE basemap_versions
		SET is_default = CASE WHEN version = ? THEN 1 ELSE 0 END, updated_at = ?
		WHERE basemap_id = ?
	`, version, toRFC3339(nowUTC()), basemapID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("version %q not found for basemap %q", version, basemapCode)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE basemaps SET updated_at = ? WHERE id = ?`, toRFC3339(nowUTC()), basemapID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ListScanRuns(ctx context.Context, limit int) ([]ScanRun, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			r.id,
			r.scan_source_id,
			s.code,
			r.status,
			r.scanned_count,
			r.added_count,
			r.updated_count,
			r.removed_count,
			r.summary_json,
			r.started_at,
			r.finished_at
		FROM scan_runs r
		JOIN scan_sources s ON s.id = r.scan_source_id
		ORDER BY r.started_at DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []ScanRun
	for rows.Next() {
		item, err := scanScanRun(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) ListPushRecords(ctx context.Context, limit int) ([]PushRecord, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			p.id,
			p.target_system_id,
			t.code,
			p.basemap_version_id,
			b.code,
			v.version,
			p.status,
			p.request_json,
			p.response_status,
			p.response_body,
			p.error_message,
			p.attempt_count,
			p.next_attempt_at,
			p.pushed_at,
			p.finished_at
		FROM push_records p
		JOIN target_systems t ON t.id = p.target_system_id
		JOIN basemap_versions v ON v.id = p.basemap_version_id
		JOIN basemaps b ON b.id = v.basemap_id
		ORDER BY p.pushed_at DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []PushRecord
	for rows.Next() {
		item, err := scanPushRecord(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
