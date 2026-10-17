package basemap

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"tile-server/api-go/internal/uid"
)

// lastUsedUpdateInterval throttles service_tokens.last_used_at writes so tile
// traffic does not issue one UPDATE per tile request.
const lastUsedUpdateInterval = time.Minute

var ErrBasemapVersionNotFound = errors.New("basemap version not found")

func (s *Store) CreateToken(ctx context.Context, input CreateTokenInput) (CreatedToken, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return CreatedToken{}, errors.New("token name is required")
	}
	// A token needs at least one capability: read grants, or the slice scope.
	hasCapability := false
	for _, value := range input.Scopes {
		if normalized := strings.ToLower(strings.TrimSpace(value)); normalized == slicePermission || normalized == readAllPermission {
			hasCapability = true
		}
	}
	if len(input.BasemapCodes) == 0 && len(input.VectorCodes) == 0 && !hasCapability {
		return CreatedToken{}, errors.New("at least one basemap code, vector code, or capability scope is required")
	}

	resolvedVectorAssets := map[string]VectorAsset{}
	for _, ref := range input.VectorCodes {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		asset, err := s.GetVectorAssetByRef(ctx, ref)
		if err != nil {
			return CreatedToken{}, err
		}
		resolvedVectorAssets[asset.Code] = asset
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CreatedToken{}, err
	}
	defer tx.Rollback()

	token, tokenHash, err := s.generateTokenSecret()
	if err != nil {
		return CreatedToken{}, err
	}

	now := nowUTC()
	// Fall back to the configured default lifetime so tokens do not live forever
	// unless an explicit expiry was supplied.
	if !input.NoExpiry && input.ExpiresAt == nil && s.cfg.TokenDefaultTTL > 0 {
		expiresAt := now.Add(s.cfg.TokenDefaultTTL)
		input.ExpiresAt = &expiresAt
	}
	tokenID := uid.New()
	prefix := token
	if len(prefix) > 16 {
		prefix = prefix[:16]
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO service_tokens (id, name, token_hash, token_prefix, scopes, status, expires_at, last_used_at, created_at)
		VALUES (?, ?, ?, ?, ?, 'active', ?, NULL, ?)
	`, tokenID, name, tokenHash, prefix, normalizeTokenScopes(input.Scopes), nullableTime(input.ExpiresAt), toRFC3339(now))
	if err != nil {
		return CreatedToken{}, err
	}

	normalizedCodes := map[string]struct{}{}
	for _, code := range input.BasemapCodes {
		normalized := normalizeBasemapCode(code)
		if normalized != "" {
			normalizedCodes[normalized] = struct{}{}
		}
	}
	if len(normalizedCodes) == 0 {
		if len(resolvedVectorAssets) == 0 && !hasCapability {
			return CreatedToken{}, errors.New("no valid basemap codes provided")
		}
	}

	for _, code := range sortedKeys(normalizedCodes) {
		var basemapID string
		err := tx.QueryRowContext(ctx, `SELECT id FROM basemaps WHERE code = ?`, code).Scan(&basemapID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return CreatedToken{}, fmt.Errorf("basemap %q not found", code)
			}
			return CreatedToken{}, err
		}
		grantID := uid.New()
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO token_grants (id, token_id, basemap_id, basemap_version_id, permission, created_at)
			VALUES (?, ?, ?, NULL, ?, ?)
		`, grantID, tokenID, basemapID, readPermission, toRFC3339(now)); err != nil {
			return CreatedToken{}, err
		}
	}

	vectorCodes := make([]string, 0, len(resolvedVectorAssets))
	for code := range resolvedVectorAssets {
		vectorCodes = append(vectorCodes, code)
	}
	sort.Strings(vectorCodes)
	for _, code := range vectorCodes {
		asset := resolvedVectorAssets[code]
		grantID := uid.New()
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO vector_token_grants (id, token_id, vector_asset_id, permission, created_at)
			VALUES (?, ?, ?, ?, ?)
		`, grantID, tokenID, asset.ID, readPermission, toRFC3339(now)); err != nil {
			return CreatedToken{}, err
		}
	}

	if err := tx.Commit(); err != nil {
		return CreatedToken{}, err
	}
	meta, err := s.getTokenByID(ctx, tokenID)
	if err != nil {
		return CreatedToken{}, err
	}
	return CreatedToken{
		Meta:  meta,
		Token: token,
	}, nil
}

func (s *Store) ListTokens(ctx context.Context) ([]ServiceToken, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, token_hash, token_prefix, scopes, status, expires_at, last_used_at, created_at
		FROM service_tokens
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []ServiceToken
	for rows.Next() {
		item, _, err := scanServiceToken(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	grantsByToken, err := s.loadAllTokenGrants(ctx)
	if err != nil {
		return nil, err
	}
	vectorGrantsByToken, err := s.loadAllVectorTokenGrants(ctx)
	if err != nil {
		return nil, err
	}
	for i := range items {
		items[i].Grants = grantsByToken[items[i].ID]
		items[i].VectorGrants = vectorGrantsByToken[items[i].ID]
	}
	return items, nil
}

func (s *Store) loadAllTokenGrants(ctx context.Context) (map[string][]TokenGrant, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			g.id,
			g.token_id,
			g.basemap_id,
			b.code,
			g.basemap_version_id,
			v.version,
			g.permission,
			g.created_at
		FROM token_grants g
		JOIN basemaps b ON b.id = g.basemap_id
		LEFT JOIN basemap_versions v ON v.id = g.basemap_version_id
		ORDER BY g.created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := map[string][]TokenGrant{}
	for rows.Next() {
		item, err := scanTokenGrant(rows)
		if err != nil {
			return nil, err
		}
		result[item.TokenID] = append(result[item.TokenID], item)
	}
	return result, rows.Err()
}

func (s *Store) loadAllVectorTokenGrants(ctx context.Context) (map[string][]VectorTokenGrant, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			g.id,
			g.token_id,
			g.vector_asset_id,
			v.code,
			g.permission,
			g.created_at
		FROM vector_token_grants g
		JOIN vector_assets v ON v.id = g.vector_asset_id
		ORDER BY g.created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := map[string][]VectorTokenGrant{}
	for rows.Next() {
		item, err := scanVectorTokenGrant(rows)
		if err != nil {
			return nil, err
		}
		result[item.TokenID] = append(result[item.TokenID], item)
	}
	return result, rows.Err()
}

func (s *Store) loadTokenGrants(ctx context.Context, tokenID string) ([]TokenGrant, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			g.id,
			g.token_id,
			g.basemap_id,
			b.code,
			g.basemap_version_id,
			v.version,
			g.permission,
			g.created_at
		FROM token_grants g
		JOIN basemaps b ON b.id = g.basemap_id
		LEFT JOIN basemap_versions v ON v.id = g.basemap_version_id
		WHERE g.token_id = ?
		ORDER BY g.created_at DESC
	`, tokenID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []TokenGrant
	for rows.Next() {
		item, err := scanTokenGrant(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) loadVectorTokenGrants(ctx context.Context, tokenID string) ([]VectorTokenGrant, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			g.id,
			g.token_id,
			g.vector_asset_id,
			v.code,
			g.permission,
			g.created_at
		FROM vector_token_grants g
		JOIN vector_assets v ON v.id = g.vector_asset_id
		WHERE g.token_id = ?
		ORDER BY g.created_at DESC
	`, tokenID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []VectorTokenGrant
	for rows.Next() {
		item, err := scanVectorTokenGrant(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) DisableToken(ctx context.Context, tokenID string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE service_tokens SET status = 'disabled' WHERE id = ?`, strings.TrimSpace(tokenID))
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("token %q not found", tokenID)
	}
	return nil
}

// DeleteToken removes a token and everything granted to it. Foreign keys
// cascade to token_grants and vector_token_grants.
func (s *Store) DeleteToken(ctx context.Context, tokenID string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM service_tokens WHERE id = ?`, strings.TrimSpace(tokenID))
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("token %q not found", tokenID)
	}
	return nil
}
func (s *Store) getTokenByID(ctx context.Context, tokenID string) (ServiceToken, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, token_hash, token_prefix, scopes, status, expires_at, last_used_at, created_at
		FROM service_tokens
		WHERE id = ?
	`, tokenID)
	item, _, err := scanServiceToken(row)
	if err != nil {
		return ServiceToken{}, err
	}
	grants, err := s.loadAllTokenGrants(ctx)
	if err != nil {
		return ServiceToken{}, err
	}
	item.Grants = grants[item.ID]
	vectorGrants, err := s.loadAllVectorTokenGrants(ctx)
	if err != nil {
		return ServiceToken{}, err
	}
	item.VectorGrants = vectorGrants[item.ID]
	return item, nil
}

func (s *Store) AuthorizeToken(ctx context.Context, rawToken string) (TokenAuth, error) {
	rawToken = strings.TrimSpace(rawToken)
	if rawToken == "" {
		return TokenAuth{}, errors.New("missing token")
	}
	sum := sha256.Sum256([]byte(rawToken))
	tokenHash := hex.EncodeToString(sum[:])

	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, token_hash, token_prefix, scopes, status, expires_at, last_used_at, created_at
		FROM service_tokens
		WHERE token_hash = ?
	`, tokenHash)
	item, _, err := scanServiceToken(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TokenAuth{}, errors.New("invalid token")
		}
		return TokenAuth{}, err
	}
	if item.Status != "active" {
		return TokenAuth{}, errors.New("token is disabled")
	}
	if item.ExpiresAt != nil && item.ExpiresAt.Before(nowUTC()) {
		return TokenAuth{}, errors.New("token is expired")
	}

	grants, err := s.loadTokenGrants(ctx, item.ID)
	if err != nil {
		return TokenAuth{}, err
	}
	item.Grants = grants
	vectorGrants, err := s.loadVectorTokenGrants(ctx, item.ID)
	if err != nil {
		return TokenAuth{}, err
	}
	item.VectorGrants = vectorGrants
	permissions := map[string]map[string]VersionScope{}
	for _, grant := range item.Grants {
		group, ok := permissions[grant.Permission]
		if !ok {
			group = map[string]VersionScope{}
			permissions[grant.Permission] = group
		}
		scope := group[grant.BasemapCode]
		if grant.BasemapVersion == nil {
			scope.AllVersions = true
			scope.Versions = nil
		} else if !scope.AllVersions {
			if scope.Versions == nil {
				scope.Versions = map[string]struct{}{}
			}
			scope.Versions[*grant.BasemapVersion] = struct{}{}
		}
		group[grant.BasemapCode] = scope
	}
	vectorPermissions := map[string]map[string]struct{}{}
	for _, grant := range item.VectorGrants {
		group, ok := vectorPermissions[grant.Permission]
		if !ok {
			group = map[string]struct{}{}
			vectorPermissions[grant.Permission] = group
		}
		group[grant.VectorCode] = struct{}{}
	}
	if item.LastUsedAt == nil || nowUTC().Sub(*item.LastUsedAt) >= lastUsedUpdateInterval {
		_, _ = s.db.ExecContext(ctx, `UPDATE service_tokens SET last_used_at = ? WHERE id = ?`, toRFC3339(nowUTC()), item.ID)
	}
	return TokenAuth{
		Token:             item,
		Permissions:       permissions,
		VectorPermissions: vectorPermissions,
		Scopes:            scopeSet(item.Scopes),
	}, nil
}

// Token scopes are coarse capabilities layered on top of per-resource grants.
// Every token can read; "slice" additionally allows submitting tile jobs.
const (
	slicePermission = "slice"
	// readAllPermission grants read on every basemap and vector, including ones
	// catalogued after the token was issued.
	readAllPermission = "read_all"
)

var allowedTokenScopes = map[string]struct{}{
	readPermission:    {},
	slicePermission:   {},
	readAllPermission: {},
}

// normalizeTokenScopes keeps only known scopes and always includes read, so the
// stored column is a stable, sorted, comma-separated list.
func normalizeTokenScopes(values []string) string {
	scopes := map[string]struct{}{readPermission: {}}
	for _, value := range values {
		scope := strings.ToLower(strings.TrimSpace(value))
		if _, ok := allowedTokenScopes[scope]; ok {
			scopes[scope] = struct{}{}
		}
	}
	return strings.Join(sortedKeys(scopes), ",")
}

func splitTokenScopes(value string) []string {
	parts := strings.Split(value, ",")
	scopes := make([]string, 0, len(parts))
	for _, part := range parts {
		scope := strings.ToLower(strings.TrimSpace(part))
		if scope != "" {
			scopes = append(scopes, scope)
		}
	}
	return scopes
}

func scopeSet(values []string) map[string]struct{} {
	scopes := make(map[string]struct{}, len(values))
	for _, value := range values {
		scopes[value] = struct{}{}
	}
	return scopes
}

// CanSlice reports whether the token may submit tile jobs.
func (auth TokenAuth) CanSlice() bool {
	_, ok := auth.Scopes[slicePermission]
	return ok
}

// CanReadAll reports whether the token carries blanket read access.
func (auth TokenAuth) CanReadAll() bool {
	_, ok := auth.Scopes[readAllPermission]
	return ok
}

func (auth TokenAuth) CanReadBasemap(code string, version string) bool {
	if auth.CanReadAll() {
		return true
	}
	group, ok := auth.Permissions[readPermission]
	if !ok {
		return false
	}
	scope, ok := group[normalizeBasemapCode(code)]
	if !ok {
		return false
	}
	if scope.AllVersions {
		return true
	}
	if version == "" {
		return len(scope.Versions) > 0
	}
	_, ok = scope.Versions[strings.TrimSpace(version)]
	return ok
}

func (s *Store) FilterCatalog(auth TokenAuth, basemaps []Basemap) []Basemap {
	filtered := make([]Basemap, 0, len(basemaps))
	for _, basemap := range basemaps {
		if !auth.CanReadBasemap(basemap.Code, "") {
			continue
		}
		copyItem := basemap
		copyItem.Versions = nil
		copyItem.Default = nil
		for _, version := range basemap.Versions {
			if auth.CanReadBasemap(basemap.Code, version.Version) {
				copyItem.Versions = append(copyItem.Versions, version)
				if version.IsDefault {
					copyVersion := version
					copyItem.Default = &copyVersion
				}
			}
		}
		if len(copyItem.Versions) == 0 {
			continue
		}
		if copyItem.Default == nil {
			copyVersion := copyItem.Versions[0]
			copyItem.Default = &copyVersion
		}
		filtered = append(filtered, copyItem)
	}
	return filtered
}

func (s *Store) ResolveTile(ctx context.Context, auth TokenAuth, basemapCode, version, tilePath string) (TileDescriptor, error) {
	basemapCode = normalizeBasemapCode(basemapCode)
	version = strings.TrimSpace(version)
	tilePath = filepath.Clean(strings.TrimPrefix(strings.TrimSpace(tilePath), "/"))
	if tilePath == "." || tilePath == "" {
		return TileDescriptor{}, errors.New("tile path is required")
	}
	if !auth.CanReadBasemap(basemapCode, version) {
		return TileDescriptor{}, errors.New("token has no access to this basemap")
	}

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
			return TileDescriptor{}, fmt.Errorf("%w: %s/%s", ErrBasemapVersionNotFound, basemapCode, version)
		}
		return TileDescriptor{}, err
	}
	versionItem := decodeBasemapVersion(versionRow)
	basemap, err := s.GetBasemapByCode(ctx, basemapCode)
	if err != nil {
		return TileDescriptor{}, err
	}

	filePath, err := safeJoin(versionItem.TileRootPath, tilePath)
	if err != nil {
		return TileDescriptor{}, err
	}
	info, err := os.Stat(filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return TileDescriptor{}, fmt.Errorf("tile %s not found", tilePath)
		}
		return TileDescriptor{}, err
	}
	if info.IsDir() {
		return TileDescriptor{}, errors.New("tile path is a directory")
	}
	contentType := mime.TypeByExtension(filepath.Ext(filePath))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return TileDescriptor{
		FilePath:     filePath,
		ContentType:  contentType,
		Basemap:      basemap,
		Version:      versionItem,
		Token:        auth.Token,
		RelativePath: tilePath,
	}, nil
}

func (s *Store) ResolveDefaultTile(ctx context.Context, auth TokenAuth, basemapCode, tilePath string) (TileDescriptor, error) {
	basemapCode = normalizeBasemapCode(basemapCode)
	if basemapCode == "" {
		return TileDescriptor{}, errors.New("basemap code is required")
	}
	if !auth.CanReadBasemap(basemapCode, "") {
		return TileDescriptor{}, errors.New("token has no access to this basemap")
	}
	version, err := s.GetDefaultBasemapVersion(ctx, basemapCode)
	if err != nil {
		return TileDescriptor{}, err
	}
	if !auth.CanReadBasemap(basemapCode, version.Version) {
		return TileDescriptor{}, errors.New("token has no access to the default basemap version")
	}
	return s.ResolveTile(ctx, auth, basemapCode, version.Version, tilePath)
}

func (s *Store) GetDefaultBasemapVersion(ctx context.Context, basemapCode string) (BasemapVersion, error) {
	basemapCode = normalizeBasemapCode(basemapCode)
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
		WHERE b.code = ?
		ORDER BY v.is_default DESC, v.version ASC
		LIMIT 1
	`, basemapCode)
	versionRow, err := scanBasemapVersionRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return BasemapVersion{}, fmt.Errorf("basemap %q has no versions", basemapCode)
		}
		return BasemapVersion{}, err
	}
	return decodeBasemapVersion(versionRow), nil
}

func safeJoin(root, relative string) (string, error) {
	root = filepath.Clean(root)
	if root == "." || root == "" {
		return "", errors.New("invalid tile root")
	}
	target := filepath.Clean(filepath.Join(root, relative))
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("invalid tile path")
	}
	return target, nil
}
