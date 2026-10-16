package basemap

import (
	"context"
	"errors"
	"os"
	"time"
)

// Availability verdicts recorded on catalog rows.
const (
	healthOK      = "ok"
	healthMissing = "missing"
	healthUnknown = "unknown"
)

// HealthReport summarizes one inspection pass.
type HealthReport struct {
	CheckedAt       time.Time `json:"checked_at"`
	VersionsChecked int       `json:"versions_checked"`
	VersionsMissing int       `json:"versions_missing"`
	VectorsChecked  int       `json:"vectors_checked"`
	VectorsMissing  int       `json:"vectors_missing"`
}

// CheckHealth stats every catalogued path and records whether it is still
// there, so a directory that was moved or deleted surfaces before a client
// hits a 404. It shares scanMu with the scanners because both rewrite catalog
// rows.
func (s *Store) CheckHealth(ctx context.Context) (HealthReport, error) {
	s.scanMu.Lock()
	defer s.scanMu.Unlock()

	checkedAt := nowUTC()
	report := HealthReport{CheckedAt: checkedAt}

	type verdict struct {
		id     string
		health string
		detail string
	}

	versions, err := s.collectHealth(ctx, `SELECT id, tile_root_path FROM basemap_versions`, true)
	if err != nil {
		return report, err
	}
	vectors, err := s.collectHealth(ctx, `SELECT id, file_path FROM vector_assets`, false)
	if err != nil {
		return report, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return report, err
	}
	defer tx.Rollback()

	stamp := toRFC3339(checkedAt)
	for _, item := range versions {
		if _, err := tx.ExecContext(ctx, `
			UPDATE basemap_versions SET health = ?, health_checked_at = ?, health_detail = ? WHERE id = ?
		`, item.health, stamp, item.detail, item.id); err != nil {
			return report, err
		}
		report.VersionsChecked++
		if item.health != healthOK {
			report.VersionsMissing++
		}
	}
	for _, item := range vectors {
		if _, err := tx.ExecContext(ctx, `
			UPDATE vector_assets SET health = ?, health_checked_at = ?, health_detail = ? WHERE id = ?
		`, item.health, stamp, item.detail, item.id); err != nil {
			return report, err
		}
		report.VectorsChecked++
		if item.health != healthOK {
			report.VectorsMissing++
		}
	}
	if err := tx.Commit(); err != nil {
		return report, err
	}
	return report, nil
}

type healthVerdict struct {
	id     string
	health string
	detail string
}

func (s *Store) collectHealth(ctx context.Context, query string, wantDir bool) ([]healthVerdict, error) {
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var verdicts []healthVerdict
	for rows.Next() {
		var id, path string
		if err := rows.Scan(&id, &path); err != nil {
			return nil, err
		}
		health, detail := inspectPath(path, wantDir)
		verdicts = append(verdicts, healthVerdict{id: id, health: health, detail: detail})
	}
	return verdicts, rows.Err()
}

func inspectPath(path string, wantDir bool) (string, string) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return healthMissing, "路径不存在"
		}
		return healthUnknown, err.Error()
	}
	if wantDir && !info.IsDir() {
		return healthMissing, "路径不是目录"
	}
	if !wantDir && info.IsDir() {
		return healthMissing, "路径是目录"
	}
	return healthOK, ""
}

// The console reads health through these maps so the row scanners on the tile
// request path stay untouched.
func (s *Store) loadVersionHealth(ctx context.Context) (map[string]ResourceHealth, error) {
	return s.loadHealth(ctx, `SELECT id, health, health_detail, health_checked_at FROM basemap_versions`)
}

func (s *Store) loadVectorHealth(ctx context.Context) (map[string]ResourceHealth, error) {
	return s.loadHealth(ctx, `SELECT id, health, health_detail, health_checked_at FROM vector_assets`)
}

func (s *Store) loadHealth(ctx context.Context, query string) (map[string]ResourceHealth, error) {
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := map[string]ResourceHealth{}
	for rows.Next() {
		var id, health, detail, checkedAt string
		if err := rows.Scan(&id, &health, &detail, &checkedAt); err != nil {
			return nil, err
		}
		result[id] = ResourceHealth{Health: health, Detail: detail, CheckedAt: parseTime(checkedAt)}
	}
	return result, rows.Err()
}
