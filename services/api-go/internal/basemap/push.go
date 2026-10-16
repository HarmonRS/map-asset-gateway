package basemap

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"tile-server/api-go/internal/uid"
)

const pushCallbackTimeout = 30 * time.Second

// Delivery outcomes. retrying is a transient state: the record stays queued
// until next_attempt_at elapses.
const (
	pushStatusSucceeded = "succeeded"
	pushStatusFailed    = "failed"
	pushStatusRetrying  = "retrying"

	// pushMaxAttempts caps how many times one record is delivered.
	pushMaxAttempts = 3
	// pushRetryBase is the first backoff step; it doubles per attempt.
	pushRetryBase = 30 * time.Second
)

func (s *Store) CreateTargetSystem(ctx context.Context, input CreateTargetSystemInput) (TargetSystem, error) {
	code := normalizeBasemapCode(input.Code)
	if code == "" {
		return TargetSystem{}, errors.New("target system code is required")
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = code
	}
	callbackURL := strings.TrimSpace(input.CallbackURL)
	if callbackURL == "" {
		return TargetSystem{}, errors.New("callback url is required")
	}
	method := strings.ToUpper(strings.TrimSpace(input.CallbackMethod))
	if method == "" {
		method = http.MethodPost
	}
	if input.CallbackHeaders == nil {
		input.CallbackHeaders = map[string]string{}
	}

	now := nowUTC()
	id := uid.Deterministic("target-system", code)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO target_systems (
			id, code, name, callback_url, callback_method, callback_headers_json, enabled, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, 1, ?, ?)
		ON CONFLICT(code) DO UPDATE SET
			name = excluded.name,
			callback_url = excluded.callback_url,
			callback_method = excluded.callback_method,
			callback_headers_json = excluded.callback_headers_json,
			enabled = 1,
			updated_at = excluded.updated_at
	`, id, code, name, callbackURL, method, writeJSON(input.CallbackHeaders), toRFC3339(now), toRFC3339(now))
	if err != nil {
		return TargetSystem{}, err
	}
	return s.GetTargetSystemByCode(ctx, code)
}

func (s *Store) GetTargetSystemByCode(ctx context.Context, code string) (TargetSystem, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, code, name, callback_url, callback_method, callback_headers_json, enabled, created_at, updated_at
		FROM target_systems
		WHERE code = ?
	`, normalizeBasemapCode(code))
	item, err := scanTargetSystem(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TargetSystem{}, fmt.Errorf("target system %q not found", code)
		}
		return TargetSystem{}, err
	}
	return item, nil
}

func (s *Store) ListTargetSystems(ctx context.Context) ([]TargetSystem, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, code, name, callback_url, callback_method, callback_headers_json, enabled, created_at, updated_at
		FROM target_systems
		ORDER BY code
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []TargetSystem
	for rows.Next() {
		item, err := scanTargetSystem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// PushBasemapVersion queues a downstream notification. The HTTP callback runs
// off the request path so a slow target cannot block the caller; the returned
// record starts as "pending" and is updated once the callback finishes.
func (s *Store) PushBasemapVersion(ctx context.Context, targetCode, basemapCode, version string) (PushRecord, error) {
	target, err := s.GetTargetSystemByCode(ctx, targetCode)
	if err != nil {
		return PushRecord{}, err
	}
	if !target.Enabled {
		return PushRecord{}, fmt.Errorf("target system %q is disabled", target.Code)
	}
	basemapCode = normalizeBasemapCode(basemapCode)
	version = strings.TrimSpace(version)

	row := s.db.QueryRowContext(ctx, `
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
		WHERE b.code = ? AND v.version = ?
	`, basemapCode, version)
	versionRow, err := scanBasemapVersionRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PushRecord{}, fmt.Errorf("basemap version %s/%s not found", basemapCode, version)
		}
		return PushRecord{}, err
	}
	versionItem := decodeBasemapVersion(versionRow)
	basemapItem, err := s.GetBasemapByCode(ctx, basemapCode)
	if err != nil {
		return PushRecord{}, err
	}

	payload := map[string]any{
		"event":      "basemap.published",
		"service":    strings.TrimSpace(s.cfg.APIBaseURL),
		"basemap":    basemapItem,
		"version":    versionItem,
		"pushed_at":  toRFC3339(nowUTC()),
		"targetCode": target.Code,
	}
	record := PushRecord{
		ID:               uid.New(),
		TargetSystemID:   target.ID,
		TargetSystemCode: target.Code,
		BasemapVersionID: versionItem.ID,
		BasemapCode:      basemapItem.Code,
		BasemapVersion:   versionItem.Version,
		Status:           "pending",
		Request:          payload,
		PushedAt:         nowUTC(),
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO push_records (
			id, target_system_id, basemap_version_id, status, request_json, response_status, response_body, error_message, pushed_at, finished_at
		) VALUES (?, ?, ?, ?, ?, NULL, '', '', ?, NULL)
	`, record.ID, record.TargetSystemID, record.BasemapVersionID, record.Status, writeJSON(record.Request), toRFC3339(record.PushedAt)); err != nil {
		return PushRecord{}, err
	}

	s.enqueuePush("push_records", record.ID, target, payload)
	return record, nil
}

func (s *Store) ListVectorPushRecords(ctx context.Context, limit int) ([]VectorPushRecord, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			p.id,
			p.target_system_id,
			t.code,
			p.vector_asset_id,
			v.code,
			p.status,
			p.request_json,
			p.response_status,
			p.response_body,
			p.error_message,
			p.attempt_count,
			p.next_attempt_at,
			p.pushed_at,
			p.finished_at
		FROM vector_push_records p
		JOIN target_systems t ON t.id = p.target_system_id
		JOIN vector_assets v ON v.id = p.vector_asset_id
		ORDER BY p.pushed_at DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []VectorPushRecord
	for rows.Next() {
		item, err := scanVectorPushRecord(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// PushVectorAsset queues a downstream notification, mirroring
// PushBasemapVersion.
func (s *Store) PushVectorAsset(ctx context.Context, targetCode, vectorCode string) (VectorPushRecord, error) {
	target, err := s.GetTargetSystemByCode(ctx, targetCode)
	if err != nil {
		return VectorPushRecord{}, err
	}
	if !target.Enabled {
		return VectorPushRecord{}, fmt.Errorf("target system %q is disabled", target.Code)
	}
	vectorCode = normalizeBasemapCode(vectorCode)
	asset, err := s.GetVectorAssetByCode(ctx, vectorCode)
	if err != nil {
		return VectorPushRecord{}, err
	}

	payload := map[string]any{
		"event":      "vector.published",
		"service":    strings.TrimSpace(s.cfg.APIBaseURL),
		"vector":     asset,
		"pushed_at":  toRFC3339(nowUTC()),
		"targetCode": target.Code,
	}
	record := VectorPushRecord{
		ID:               uid.New(),
		TargetSystemID:   target.ID,
		TargetSystemCode: target.Code,
		VectorAssetID:    asset.ID,
		VectorCode:       asset.Code,
		Status:           "pending",
		Request:          payload,
		PushedAt:         nowUTC(),
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO vector_push_records (
			id, target_system_id, vector_asset_id, status, request_json, response_status, response_body, error_message, pushed_at, finished_at
		) VALUES (?, ?, ?, ?, ?, NULL, '', '', ?, NULL)
	`, record.ID, record.TargetSystemID, record.VectorAssetID, record.Status, writeJSON(record.Request), toRFC3339(record.PushedAt)); err != nil {
		return VectorPushRecord{}, err
	}

	s.enqueuePush("vector_push_records", record.ID, target, payload)
	return record, nil
}

// enqueuePush performs the first delivery attempt off the request path. A
// failure inside the retry budget is rescheduled by dispatchPushRetries.
func (s *Store) enqueuePush(table, pushID string, target TargetSystem, payload map[string]any) {
	s.pushWG.Add(1)
	go func() {
		defer s.pushWG.Done()
		s.attemptPush(table, pushID, 1, target, payload)
	}()
}

// attemptPush performs one delivery attempt and records the outcome. attempt is
// the 1-based attempt number; table is a trusted internal literal.
func (s *Store) attemptPush(table, pushID string, attempt int, target TargetSystem, payload map[string]any) {
	ctx, cancel := context.WithTimeout(context.Background(), pushCallbackTimeout)
	defer cancel()

	status, body, err := s.deliverCallback(ctx, target, payload)
	errorMessage := ""
	if err != nil {
		errorMessage = err.Error()
	}
	var responseStatus any
	if status != 0 {
		responseStatus = status
	}

	if err != nil && attempt < pushMaxAttempts {
		// Stay queued: the dispatcher picks it up once the backoff elapses.
		next := nowUTC().Add(pushRetryDelay(attempt))
		if _, updateErr := s.db.ExecContext(context.Background(), fmt.Sprintf(`
			UPDATE %s
			SET status = ?, attempt_count = ?, next_attempt_at = ?, response_status = ?, response_body = ?,
				error_message = ?, finished_at = NULL
			WHERE id = ?
		`, table), pushStatusRetrying, attempt, toRFC3339(next), responseStatus, body, errorMessage, pushID); updateErr != nil {
			log.Printf("reschedule push %s/%s: %v", table, pushID, updateErr)
		}
		return
	}

	recordStatus := pushStatusSucceeded
	if err != nil {
		recordStatus = pushStatusFailed
	}
	if _, updateErr := s.db.ExecContext(context.Background(), fmt.Sprintf(`
		UPDATE %s
		SET status = ?, attempt_count = ?, next_attempt_at = NULL, response_status = ?, response_body = ?,
			error_message = ?, finished_at = ?
		WHERE id = ?
	`, table), recordStatus, attempt, responseStatus, body, errorMessage, toRFC3339(nowUTC()), pushID); updateErr != nil {
		log.Printf("record push %s/%s: %v", table, pushID, updateErr)
	}
}

// pushRetryDelay backs off exponentially: 30s, 60s, ...
func pushRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	return pushRetryBase * time.Duration(1<<(attempt-1))
}

// dispatchPushRetries finds attempts whose backoff has elapsed and runs them.
func (s *Store) dispatchPushRetries(ctx context.Context) error {
	for _, table := range []string{"push_records", "vector_push_records"} {
		due, err := s.dueRetries(ctx, table, 5)
		if err != nil {
			return err
		}
		for _, item := range due {
			target, err := s.GetTargetSystemByID(ctx, item.targetID)
			if err != nil {
				s.failRetry(table, item.id, fmt.Sprintf("target system is gone: %v", err))
				continue
			}
			if !target.Enabled {
				s.failRetry(table, item.id, "target system is disabled")
				continue
			}
			s.attemptPush(table, item.id, item.attempt+1, target, item.payload)
		}
	}
	return nil
}

type duePush struct {
	id       string
	attempt  int
	targetID string
	payload  map[string]any
}

func (s *Store) dueRetries(ctx context.Context, table string, limit int) ([]duePush, error) {
	now := toRFC3339(nowUTC())
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT id, attempt_count, target_system_id, request_json
		FROM %s
		WHERE status = ? AND (next_attempt_at IS NULL OR next_attempt_at <= ?)
		ORDER BY next_attempt_at
		LIMIT ?
	`, table), pushStatusRetrying, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []duePush
	for rows.Next() {
		var item duePush
		var requestJSON string
		if err := rows.Scan(&item.id, &item.attempt, &item.targetID, &requestJSON); err != nil {
			return nil, err
		}
		if strings.TrimSpace(requestJSON) != "" {
			if err := readMaybeJSON(requestJSON, &item.payload); err != nil {
				item.payload = nil
			}
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) failRetry(table, pushID, message string) {
	if _, err := s.db.ExecContext(context.Background(), fmt.Sprintf(`
		UPDATE %s SET status = ?, error_message = ?, next_attempt_at = NULL, finished_at = ? WHERE id = ?
	`, table), pushStatusFailed, message, toRFC3339(nowUTC()), pushID); err != nil {
		log.Printf("fail push %s/%s: %v", table, pushID, err)
	}
}

// GetTargetSystemByID looks a target up by primary key for retry dispatch.
func (s *Store) GetTargetSystemByID(ctx context.Context, id string) (TargetSystem, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, code, name, callback_url, callback_method, callback_headers_json, enabled, created_at, updated_at
		FROM target_systems
		WHERE id = ?
	`, strings.TrimSpace(id))
	item, err := scanTargetSystem(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TargetSystem{}, fmt.Errorf("target system %q not found", id)
		}
		return TargetSystem{}, err
	}
	return item, nil
}

// RetryPush re-queues a finished push record. kind is "basemap" or "vector".
func (s *Store) RetryPush(ctx context.Context, kind, recordID string) error {
	table := "push_records"
	if kind == "vector" {
		table = "vector_push_records"
	} else if kind != "basemap" {
		return fmt.Errorf("unknown push kind %q", kind)
	}
	result, err := s.db.ExecContext(ctx, fmt.Sprintf(`
		UPDATE %s
		SET status = ?, attempt_count = 0, next_attempt_at = ?, finished_at = NULL, error_message = ''
		WHERE id = ?
	`, table), pushStatusRetrying, toRFC3339(nowUTC()), strings.TrimSpace(recordID))
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("push record %q not found", recordID)
	}
	// Kick the dispatcher so a manual retry does not wait for the next tick.
	s.pushWG.Add(1)
	go func() {
		defer s.pushWG.Done()
		_ = s.dispatchPushRetries(context.Background())
	}()
	return nil
}

// deliverCallback performs the downstream HTTP call for a target system.
func (s *Store) deliverCallback(ctx context.Context, target TargetSystem, payload map[string]any) (int, string, error) {
	return s.postJSON(ctx, target.CallbackMethod, target.CallbackURL, target.CallbackHeaders, payload)
}

// postJSON sends a JSON payload and returns the status code plus a truncated
// response body. Shared by push callbacks and tile-job completion callbacks.
func (s *Store) postJSON(ctx context.Context, method, url string, headers map[string]string, payload map[string]any) (int, string, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, "", err
	}
	request, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return 0, "", err
	}
	request.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		request.Header.Set(key, value)
	}

	response, err := s.httpClient.Do(request)
	if err != nil {
		return 0, "", err
	}
	defer response.Body.Close()

	responseBody := limitedReadAll(response.Body, 256*1024)
	// Drain whatever is left so the connection can be reused.
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return response.StatusCode, responseBody, fmt.Errorf("target returned status %d", response.StatusCode)
	}
	return response.StatusCode, responseBody, nil
}
