package basemap

import (
	"database/sql"
)

func scanVectorAsset(scanner interface {
	Scan(dest ...any) error
}) (VectorAsset, error) {
	var item VectorAsset
	var metadataJSON string
	var createdAt string
	var updatedAt string
	if err := scanner.Scan(
		&item.ID,
		&item.Code,
		&item.Name,
		&item.Format,
		&item.Status,
		&item.Description,
		&item.FilePath,
		&item.FileName,
		&item.FileSize,
		&item.Checksum,
		&item.DownloadURL,
		&metadataJSON,
		&createdAt,
		&updatedAt,
	); err != nil {
		return VectorAsset{}, err
	}
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

func scanVectorTokenGrant(scanner interface {
	Scan(dest ...any) error
}) (VectorTokenGrant, error) {
	var item VectorTokenGrant
	var createdAt string
	if err := scanner.Scan(
		&item.ID,
		&item.TokenID,
		&item.VectorID,
		&item.VectorCode,
		&item.Permission,
		&createdAt,
	); err != nil {
		return VectorTokenGrant{}, err
	}
	item.CreatedAt = parseTime(createdAt)
	return item, nil
}

func scanVectorPushRecord(scanner interface {
	Scan(dest ...any) error
}) (VectorPushRecord, error) {
	var item VectorPushRecord
	var requestJSON string
	var responseStatus sql.NullInt64
	var nextAttemptAt sql.NullString
	var finishedAt sql.NullString
	var pushedAt string
	if err := scanner.Scan(
		&item.ID,
		&item.TargetSystemID,
		&item.TargetSystemCode,
		&item.VectorAssetID,
		&item.VectorCode,
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
		return VectorPushRecord{}, err
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
