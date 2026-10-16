package basemap

import (
	"database/sql"
	"strings"
	"time"
)

type basemapVersionRow struct {
	ID           string
	BasemapID    string
	BasemapCode  string
	Version      string
	Status       string
	IsDefault    bool
	ManifestPath string
	TileRootPath string
	URLTemplate  string
	TileFormat   string
	TileScheme   string
	MinZoom      int
	MaxZoom      int
	BBoxJSON     string
	Attribution  string
	MetadataJSON string
	CreatedAt    string
	UpdatedAt    string
}

func parseTime(value string) time.Time {
	if strings.TrimSpace(value) == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func scanBasemapVersionRow(scanner interface {
	Scan(dest ...any) error
}) (basemapVersionRow, error) {
	var row basemapVersionRow
	var isDefault int
	if err := scanner.Scan(
		&row.ID,
		&row.BasemapID,
		&row.BasemapCode,
		&row.Version,
		&row.Status,
		&isDefault,
		&row.ManifestPath,
		&row.TileRootPath,
		&row.URLTemplate,
		&row.TileFormat,
		&row.TileScheme,
		&row.MinZoom,
		&row.MaxZoom,
		&row.BBoxJSON,
		&row.Attribution,
		&row.MetadataJSON,
		&row.CreatedAt,
		&row.UpdatedAt,
	); err != nil {
		return basemapVersionRow{}, err
	}
	row.IsDefault = isDefault == 1
	return row, nil
}

func decodeBasemapVersion(row basemapVersionRow) BasemapVersion {
	item := BasemapVersion{
		ID:           row.ID,
		BasemapID:    row.BasemapID,
		BasemapCode:  row.BasemapCode,
		Version:      row.Version,
		Status:       row.Status,
		IsDefault:    row.IsDefault,
		ManifestPath: row.ManifestPath,
		TileRootPath: row.TileRootPath,
		URLTemplate:  row.URLTemplate,
		TileFormat:   row.TileFormat,
		TileScheme:   row.TileScheme,
		MinZoom:      row.MinZoom,
		MaxZoom:      row.MaxZoom,
		Attribution:  row.Attribution,
		CreatedAt:    parseTime(row.CreatedAt),
		UpdatedAt:    parseTime(row.UpdatedAt),
	}
	if strings.TrimSpace(row.BBoxJSON) != "" {
		_ = readMaybeJSON(row.BBoxJSON, &item.BBox)
	}
	if strings.TrimSpace(row.MetadataJSON) != "" {
		_ = readMaybeJSON(row.MetadataJSON, &item.Metadata)
	}
	if item.Metadata == nil {
		item.Metadata = map[string]any{}
	}
	return item
}

func scanBasemap(scanner interface {
	Scan(dest ...any) error
}) (Basemap, error) {
	var item Basemap
	var createdAt string
	var updatedAt string
	if err := scanner.Scan(
		&item.ID,
		&item.Code,
		&item.Name,
		&item.Type,
		&item.Status,
		&item.Description,
		&createdAt,
		&updatedAt,
	); err != nil {
		return Basemap{}, err
	}
	item.CreatedAt = parseTime(createdAt)
	item.UpdatedAt = parseTime(updatedAt)
	return item, nil
}

func scanScanSource(scanner interface {
	Scan(dest ...any) error
}) (ScanSource, error) {
	var item ScanSource
	var enabled int
	var metadataJSON string
	var createdAt string
	var updatedAt string
	if err := scanner.Scan(
		&item.ID,
		&item.Code,
		&item.Name,
		&item.RootPath,
		&item.ManifestName,
		&enabled,
		&metadataJSON,
		&createdAt,
		&updatedAt,
	); err != nil {
		return ScanSource{}, err
	}
	item.Enabled = enabled == 1
	item.CreatedAt = parseTime(createdAt)
	item.UpdatedAt = parseTime(updatedAt)
	if metadataJSON != "" {
		_ = readMaybeJSON(metadataJSON, &item.Metadata)
	}
	if item.Metadata == nil {
		item.Metadata = map[string]any{}
	}
	return item, nil
}

func scanScanRun(scanner interface {
	Scan(dest ...any) error
}) (ScanRun, error) {
	var item ScanRun
	var summaryJSON string
	var startedAt string
	var finishedAt string
	if err := scanner.Scan(
		&item.ID,
		&item.ScanSourceID,
		&item.SourceCode,
		&item.Status,
		&item.ScannedCount,
		&item.AddedCount,
		&item.UpdatedCount,
		&item.RemovedCount,
		&summaryJSON,
		&startedAt,
		&finishedAt,
	); err != nil {
		return ScanRun{}, err
	}
	item.StartedAt = parseTime(startedAt)
	item.FinishedAt = parseTime(finishedAt)
	if summaryJSON != "" {
		_ = readMaybeJSON(summaryJSON, &item.Summary)
	}
	if item.Summary == nil {
		item.Summary = map[string]any{}
	}
	return item, nil
}

func scanTargetSystem(scanner interface {
	Scan(dest ...any) error
}) (TargetSystem, error) {
	var item TargetSystem
	var enabled int
	var headersJSON string
	var createdAt string
	var updatedAt string
	if err := scanner.Scan(
		&item.ID,
		&item.Code,
		&item.Name,
		&item.CallbackURL,
		&item.CallbackMethod,
		&headersJSON,
		&enabled,
		&createdAt,
		&updatedAt,
	); err != nil {
		return TargetSystem{}, err
	}
	item.Enabled = enabled == 1
	item.CreatedAt = parseTime(createdAt)
	item.UpdatedAt = parseTime(updatedAt)
	if headersJSON != "" {
		_ = readMaybeJSON(headersJSON, &item.CallbackHeaders)
	}
	if item.CallbackHeaders == nil {
		item.CallbackHeaders = map[string]string{}
	}
	return item, nil
}

func scanServiceToken(scanner interface {
	Scan(dest ...any) error
}) (ServiceToken, string, error) {
	var item ServiceToken
	var tokenHash string
	var scopes string
	var expiresAt sql.NullString
	var lastUsedAt sql.NullString
	var createdAt string
	if err := scanner.Scan(
		&item.ID,
		&item.Name,
		&tokenHash,
		&item.TokenPrefix,
		&scopes,
		&item.Status,
		&expiresAt,
		&lastUsedAt,
		&createdAt,
	); err != nil {
		return ServiceToken{}, "", err
	}
	item.CreatedAt = parseTime(createdAt)
	item.Scopes = splitTokenScopes(scopes)
	if expiresAt.Valid {
		value := parseTime(expiresAt.String)
		item.ExpiresAt = &value
	}
	if lastUsedAt.Valid {
		value := parseTime(lastUsedAt.String)
		item.LastUsedAt = &value
	}
	return item, tokenHash, nil
}

func scanTokenGrant(scanner interface {
	Scan(dest ...any) error
}) (TokenGrant, error) {
	var item TokenGrant
	var versionID sql.NullString
	var version sql.NullString
	var createdAt string
	if err := scanner.Scan(
		&item.ID,
		&item.TokenID,
		&item.BasemapID,
		&item.BasemapCode,
		&versionID,
		&version,
		&item.Permission,
		&createdAt,
	); err != nil {
		return TokenGrant{}, err
	}
	if versionID.Valid {
		item.BasemapVersionID = &versionID.String
	}
	if version.Valid {
		item.BasemapVersion = &version.String
	}
	item.CreatedAt = parseTime(createdAt)
	return item, nil
}

func scanPushRecord(scanner interface {
	Scan(dest ...any) error
}) (PushRecord, error) {
	var item PushRecord
	var requestJSON string
	var responseStatus sql.NullInt64
	var nextAttemptAt sql.NullString
	var finishedAt sql.NullString
	var pushedAt string
	if err := scanner.Scan(
		&item.ID,
		&item.TargetSystemID,
		&item.TargetSystemCode,
		&item.BasemapVersionID,
		&item.BasemapCode,
		&item.BasemapVersion,
		&item.Status,
		&requestJSON,
		&responseStatus,
		&item.ResponseBody,
		&item.ErrorMessage,
		&item.AttemptCount,
		&nextAttemptAt,
		&pushedAt,
		&finishedAt,
	); err != nil {
		return PushRecord{}, err
	}
	if requestJSON != "" {
		_ = readMaybeJSON(requestJSON, &item.Request)
	}
	if item.Request == nil {
		item.Request = map[string]any{}
	}
	if responseStatus.Valid {
		value := int(responseStatus.Int64)
		item.ResponseStatus = &value
	}
	if nextAttemptAt.Valid {
		value := parseTime(nextAttemptAt.String)
		item.NextAttemptAt = &value
	}
	item.PushedAt = parseTime(pushedAt)
	if finishedAt.Valid {
		value := parseTime(finishedAt.String)
		item.FinishedAt = &value
	}
	return item, nil
}
