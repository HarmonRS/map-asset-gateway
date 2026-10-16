package basemap

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"tile-server/api-go/internal/uid"
)

type VectorDownloadDescriptor struct {
	Asset        VectorAsset
	FilePath     string
	ContentType  string
	FileName     string
	Zipped       bool
	PackageFiles []string
}

func (s *Store) ListVectorAssets(ctx context.Context) ([]VectorAsset, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, code, name, format, status, description, file_path, file_name, file_size, checksum, download_url, metadata_json, created_at, updated_at
		FROM vector_assets
		ORDER BY code
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []VectorAsset
	for rows.Next() {
		item, err := scanVectorAsset(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) GetVectorAssetByCode(ctx context.Context, code string) (VectorAsset, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, code, name, format, status, description, file_path, file_name, file_size, checksum, download_url, metadata_json, created_at, updated_at
		FROM vector_assets
		WHERE code = ?
	`, normalizeBasemapCode(code))
	item, err := scanVectorAsset(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return VectorAsset{}, fmt.Errorf("vector asset %q not found", code)
		}
		return VectorAsset{}, err
	}
	return item, nil
}

func (s *Store) GetVectorAssetByRef(ctx context.Context, ref string) (VectorAsset, error) {
	cleaned, err := cleanVectorAssetRef(ref)
	if err != nil {
		return VectorAsset{}, err
	}

	// The compatibility routes resolve by ref on every request, so try the
	// indexed single-row lookups before falling back to a full table scan.
	if item, ok, err := s.getVectorAssetByLookup(ctx, "code = ?", normalizeBasemapCode(cleaned)); err != nil {
		return VectorAsset{}, err
	} else if ok {
		return item, nil
	}
	if item, ok, err := s.getVectorAssetByLookup(ctx, "file_name = ? COLLATE NOCASE", path.Base(cleaned)); err != nil {
		return VectorAsset{}, err
	} else if ok {
		return item, nil
	}

	items, err := s.ListVectorAssets(ctx)
	if err != nil {
		return VectorAsset{}, err
	}
	for _, item := range items {
		if vectorAssetMatchesRef(item, cleaned) {
			return item, nil
		}
	}
	return VectorAsset{}, fmt.Errorf("vector asset %q not found", ref)
}

// getVectorAssetByLookup runs a single indexed lookup. where must be a trusted
// internal literal, never user input.
func (s *Store) getVectorAssetByLookup(ctx context.Context, where, value string) (VectorAsset, bool, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return VectorAsset{}, false, nil
	}
	query := fmt.Sprintf(`
		SELECT id, code, name, format, status, description, file_path, file_name, file_size, checksum, download_url, metadata_json, created_at, updated_at
		FROM vector_assets
		WHERE %s
		LIMIT 1
	`, where)
	item, err := scanVectorAsset(s.db.QueryRowContext(ctx, query, value))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return VectorAsset{}, false, nil
		}
		return VectorAsset{}, false, err
	}
	return item, true, nil
}

func (s *Store) RunVectorScan(ctx context.Context) (map[string]any, error) {
	root := strings.TrimSpace(s.cfg.VectorScanRoot)
	if root == "" {
		return nil, nil
	}
	if !s.scanMu.TryLock() {
		return nil, ErrScanInProgress
	}
	defer s.scanMu.Unlock()

	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}

	hashCache, err := s.loadVectorHashCache(ctx)
	if err != nil {
		return nil, err
	}

	items, err := scanVectorRoot(root, hashCache)
	if err != nil {
		return nil, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	existing, err := loadExistingVectors(ctx, tx)
	if err != nil {
		return nil, err
	}

	found := map[string]struct{}{}
	added, updated, unchanged := 0, 0, 0
	now := nowUTC()
	for _, item := range items {
		found[item.Code] = struct{}{}
		if prev, ok := existing[item.Code]; ok {
			if s.scannedVectorMatches(item, prev) {
				unchanged++
			} else {
				updated++
			}
		} else {
			added++
		}
		if err := s.upsertVectorAssetTx(ctx, tx, item, now); err != nil {
			return nil, err
		}
	}

	removed := 0
	for code := range existing {
		if _, ok := found[code]; ok {
			continue
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM vector_assets WHERE code = ?`, code); err != nil {
			return nil, err
		}
		removed++
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return map[string]any{
		"scan_root":       root,
		"scanned_count":   len(items),
		"added_count":     added,
		"updated_count":   updated,
		"unchanged_count": unchanged,
		"removed_count":   removed,
	}, nil
}

// existingVector is a snapshot of a stored asset, used to tell a real update
// from a no-op rescan.
type existingVector struct {
	Name         string
	Format       string
	Status       string
	Description  string
	FilePath     string
	FileName     string
	FileSize     int64
	Checksum     string
	DownloadURL  string
	MetadataJSON string
}

func loadExistingVectors(ctx context.Context, tx *sql.Tx) (map[string]existingVector, error) {
	rows, err := tx.QueryContext(ctx, `
        SELECT code, name, format, status, description, file_path, file_name, file_size, checksum, download_url, metadata_json
        FROM vector_assets
    `)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := map[string]existingVector{}
	for rows.Next() {
		var code string
		var item existingVector
		if err := rows.Scan(
			&code,
			&item.Name,
			&item.Format,
			&item.Status,
			&item.Description,
			&item.FilePath,
			&item.FileName,
			&item.FileSize,
			&item.Checksum,
			&item.DownloadURL,
			&item.MetadataJSON,
		); err != nil {
			return nil, err
		}
		items[code] = item
	}
	return items, rows.Err()
}

func (s *Store) scannedVectorMatches(item scannedVector, prev existingVector) bool {
	return item.Name == prev.Name &&
		item.Format == prev.Format &&
		item.Status == prev.Status &&
		item.Description == prev.Description &&
		item.FilePath == prev.FilePath &&
		item.FileName == prev.FileName &&
		item.FileSize == prev.FileSize &&
		item.Checksum == prev.Checksum &&
		s.buildVectorDownloadURL(item.Code) == prev.DownloadURL &&
		writeJSON(item.Metadata) == prev.MetadataJSON
}

// vectorHashEntry lets an unchanged file reuse the checksum recorded by the
// previous scan instead of re-reading the whole file on every startup.
type vectorHashEntry struct {
	Size     int64
	ModTime  int64
	Checksum string
}

func (s *Store) loadVectorHashCache(ctx context.Context) (map[string]vectorHashEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
        SELECT file_path, checksum, metadata_json
        FROM vector_assets
    `)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cache := map[string]vectorHashEntry{}
	for rows.Next() {
		var filePath, checksum, metadataJSON string
		if err := rows.Scan(&filePath, &checksum, &metadataJSON); err != nil {
			return nil, err
		}
		if strings.TrimSpace(checksum) == "" {
			continue
		}
		var meta struct {
			HashSize  int64 `json:"hash_size"`
			HashMTime int64 `json:"hash_mtime"`
		}
		_ = readMaybeJSON(metadataJSON, &meta)
		if meta.HashSize == 0 && meta.HashMTime == 0 {
			continue
		}
		cache[filePath] = vectorHashEntry{Size: meta.HashSize, ModTime: meta.HashMTime, Checksum: checksum}
	}
	return cache, rows.Err()
}

type scannedVector struct {
	Code        string
	Name        string
	Format      string
	Status      string
	Description string
	FilePath    string
	FileName    string
	FileSize    int64
	Checksum    string
	Metadata    map[string]any
}

func scanVectorRoot(root string, hashCache map[string]vectorHashEntry) ([]scannedVector, error) {
	shapefiles := map[string]string{}
	var items []scannedVector
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		switch ext {
		case ".geojson", ".json", ".zip":
			item, err := buildScannedVector(root, path, ext, hashCache)
			if err != nil {
				return err
			}
			items = append(items, item)
		case ".shp":
			base := strings.TrimSuffix(path, filepath.Ext(path))
			shapefiles[base] = path
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	for _, base := range sortedKeysStringMap(shapefiles) {
		path := shapefiles[base]
		item, err := buildScannedVector(root, path, ".shp", hashCache)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}

	items = disambiguateVectorCodes(items)
	sort.Slice(items, func(i, j int) bool {
		return items[i].Code < items[j].Code
	})
	return items, nil
}

// disambiguateVectorCodes keeps both assets when two different relative paths
// normalize to the same code (for example "a/b.geojson" and "a-b.geojson").
// The suffix comes from the relative path, so it is stable across scans.
func disambiguateVectorCodes(items []scannedVector) []scannedVector {
	byCode := map[string][]int{}
	for i, item := range items {
		byCode[item.Code] = append(byCode[item.Code], i)
	}
	for code, indexes := range byCode {
		if len(indexes) < 2 {
			continue
		}
		for _, index := range indexes {
			sum := sha256.Sum256([]byte(scannedVectorRelPath(items[index])))
			items[index].Code = code + "-" + hex.EncodeToString(sum[:4])
		}
	}
	return items
}

func scannedVectorRelPath(item scannedVector) string {
	if item.Metadata == nil {
		return ""
	}
	value, ok := item.Metadata["relative_path"].(string)
	if !ok {
		return ""
	}
	return value
}

func sortedKeysStringMap(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func buildScannedVector(root, path, ext string, hashCache map[string]vectorHashEntry) (scannedVector, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return scannedVector{}, err
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return scannedVector{}, err
	}
	rel, err := filepath.Rel(root, absPath)
	if err != nil {
		return scannedVector{}, err
	}
	code := vectorCodeFromRel(rel, ext)
	name := strings.TrimSuffix(filepath.Base(absPath), filepath.Ext(absPath))
	format := vectorFormatFromExt(ext)
	fileName := filepath.Base(absPath)
	fileSize := info.Size()

	hashSize, hashMTime, err := vectorHashStamp(absPath, ext)
	if err != nil {
		return scannedVector{}, err
	}
	checksum := ""
	if cached, ok := hashCache[absPath]; ok &&
		(hashSize > 0 || hashMTime > 0) &&
		cached.Size == hashSize && cached.ModTime == hashMTime {
		checksum = cached.Checksum
	}
	if checksum == "" {
		checksum, err = hashVector(absPath, ext)
		if err != nil {
			return scannedVector{}, err
		}
	}

	if format == "shapefile" {
		fileName = name + ".zip"
	}
	return scannedVector{
		Code:     code,
		Name:     name,
		Format:   format,
		Status:   "ready",
		FilePath: absPath,
		FileName: fileName,
		FileSize: fileSize,
		Checksum: checksum,
		Metadata: map[string]any{
			"relative_path": filepath.ToSlash(rel),
			"hash_size":     hashSize,
			"hash_mtime":    hashMTime,
		},
	}, nil
}

// vectorHashStamp summarizes the inputs that feed the checksum, so an unchanged
// file can reuse the previous checksum instead of being rehashed every startup.
// Shapefiles fold in every sidecar so a changed .dbf/.shx also invalidates it.
func vectorHashStamp(absPath, ext string) (int64, int64, error) {
	if strings.EqualFold(ext, ".shp") {
		var totalSize, maxModTime int64
		for _, sibling := range shapefilePackageFiles(absPath) {
			info, err := os.Stat(sibling)
			if err != nil || info.IsDir() {
				continue
			}
			totalSize += info.Size()
			if modTime := info.ModTime().Unix(); modTime > maxModTime {
				maxModTime = modTime
			}
		}
		return totalSize, maxModTime, nil
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return 0, 0, err
	}
	return info.Size(), info.ModTime().Unix(), nil
}

func vectorCodeFromRel(rel, ext string) string {
	rel = filepath.ToSlash(rel)
	rel = strings.TrimSuffix(rel, strings.ToLower(filepath.Ext(rel)))
	rel = strings.TrimSuffix(rel, filepath.Ext(rel))
	rel = strings.ReplaceAll(rel, "/", "-")
	code := normalizeBasemapCode(rel)
	if code != "" {
		return code
	}
	sum := sha256.Sum256([]byte(rel))
	return "vector-" + hex.EncodeToString(sum[:6])
}

func vectorFormatFromExt(ext string) string {
	switch strings.ToLower(ext) {
	case ".geojson", ".json":
		return "geojson"
	case ".shp":
		return "shapefile"
	case ".zip":
		return "shpzip"
	default:
		return strings.TrimPrefix(strings.ToLower(ext), ".")
	}
}

func hashVector(path, ext string) (string, error) {
	sum := sha256.New()
	if strings.EqualFold(ext, ".shp") {
		for _, sibling := range shapefilePackageFiles(path) {
			info, err := os.Stat(sibling)
			if err != nil || info.IsDir() {
				continue
			}
			if _, err := io.WriteString(sum, filepath.Base(sibling)); err != nil {
				return "", err
			}
			if err := hashFileInto(sum, sibling); err != nil {
				return "", err
			}
		}
		return hex.EncodeToString(sum.Sum(nil)), nil
	}
	if err := hashFileInto(sum, path); err != nil {
		return "", err
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

func hashFileInto(writer io.Writer, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = io.Copy(writer, file)
	return err
}

func shapefilePackageFiles(path string) []string {
	base := strings.TrimSuffix(path, filepath.Ext(path))
	candidates := []string{".shp", ".dbf", ".shx", ".prj", ".cpg", ".qix", ".sbn", ".sbx"}
	files := make([]string, 0, len(candidates))
	for _, ext := range candidates {
		file := base + ext
		if _, err := os.Stat(file); err == nil {
			files = append(files, file)
		}
	}
	sort.Strings(files)
	return files
}

func (s *Store) upsertVectorAssetTx(ctx context.Context, tx *sql.Tx, item scannedVector, now time.Time) error {
	id := uid.Deterministic("vector-asset", item.Code)
	_, err := tx.ExecContext(ctx, `
		INSERT INTO vector_assets (
			id, code, name, format, status, description, file_path, file_name, file_size, checksum, download_url, metadata_json, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(code) DO UPDATE SET
			name = excluded.name,
			format = excluded.format,
			status = excluded.status,
			description = excluded.description,
			file_path = excluded.file_path,
			file_name = excluded.file_name,
			file_size = excluded.file_size,
			checksum = excluded.checksum,
			download_url = excluded.download_url,
			metadata_json = excluded.metadata_json,
			updated_at = excluded.updated_at
	`, id, item.Code, item.Name, item.Format, item.Status, item.Description, item.FilePath, item.FileName, item.FileSize, item.Checksum, s.buildVectorDownloadURL(item.Code), writeJSON(item.Metadata), toRFC3339(now), toRFC3339(now))
	return err
}

func (s *Store) buildVectorDownloadURL(code string) string {
	base := trimURL(s.cfg.VectorBaseURL)
	if base == "" {
		base = trimURL(s.cfg.APIBaseURL)
	}
	return fmt.Sprintf("%s/vectors/%s/download", base, code)
}

func (auth TokenAuth) CanReadVector(code string) bool {
	group, ok := auth.VectorPermissions[readPermission]
	if !ok {
		return false
	}
	_, ok = group[normalizeBasemapCode(code)]
	return ok
}

func (s *Store) FilterVectors(auth TokenAuth, items []VectorAsset) []VectorAsset {
	filtered := make([]VectorAsset, 0, len(items))
	for _, item := range items {
		if auth.CanReadVector(item.Code) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func (s *Store) ResolveVectorDownload(ctx context.Context, auth TokenAuth, code string) (VectorDownloadDescriptor, error) {
	code = normalizeBasemapCode(code)
	if !auth.CanReadVector(code) {
		return VectorDownloadDescriptor{}, errors.New("token has no access to this vector asset")
	}
	item, err := s.GetVectorAssetByCode(ctx, code)
	if err != nil {
		return VectorDownloadDescriptor{}, err
	}
	return buildVectorDownloadDescriptor(item)
}

func (s *Store) ResolveVectorDownloadByRef(ctx context.Context, auth TokenAuth, ref string, allowedFormats ...string) (VectorDownloadDescriptor, error) {
	item, err := s.GetVectorAssetByRef(ctx, ref)
	if err != nil {
		return VectorDownloadDescriptor{}, err
	}
	if !auth.CanReadVector(item.Code) {
		return VectorDownloadDescriptor{}, errors.New("token has no access to this vector asset")
	}
	if !vectorFormatAllowed(item.Format, allowedFormats) {
		return VectorDownloadDescriptor{}, fmt.Errorf("vector asset %q is not available in requested format", ref)
	}
	return buildVectorDownloadDescriptor(item)
}

func buildVectorDownloadDescriptor(item VectorAsset) (VectorDownloadDescriptor, error) {
	switch item.Format {
	case "geojson":
		contentType := mime.TypeByExtension(filepath.Ext(item.FilePath))
		if contentType == "" {
			contentType = "application/geo+json"
		}
		return VectorDownloadDescriptor{
			Asset:       item,
			FilePath:    item.FilePath,
			FileName:    item.FileName,
			ContentType: contentType,
		}, nil
	case "shapefile":
		files := shapefilePackageFiles(item.FilePath)
		if len(files) == 0 {
			return VectorDownloadDescriptor{}, errors.New("shapefile package is missing sidecar files")
		}
		return VectorDownloadDescriptor{
			Asset:        item,
			FileName:     item.FileName,
			ContentType:  "application/zip",
			Zipped:       true,
			PackageFiles: files,
		}, nil
	case "shpzip":
		return VectorDownloadDescriptor{
			Asset:       item,
			FilePath:    item.FilePath,
			FileName:    item.FileName,
			ContentType: "application/zip",
		}, nil
	default:
		contentType := mime.TypeByExtension(filepath.Ext(item.FilePath))
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		return VectorDownloadDescriptor{
			Asset:       item,
			FilePath:    item.FilePath,
			FileName:    item.FileName,
			ContentType: contentType,
		}, nil
	}
}

func cleanVectorAssetRef(value string) (string, error) {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	value = strings.TrimPrefix(value, "/")
	if value == "" {
		return "", errors.New("vector asset path is required")
	}
	cleaned := path.Clean(value)
	if cleaned == "." || cleaned == "/" || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", errors.New("invalid vector asset path")
	}
	return strings.TrimPrefix(cleaned, "./"), nil
}

func vectorAssetMatchesRef(item VectorAsset, ref string) bool {
	for _, candidate := range vectorAssetRefs(item) {
		if strings.EqualFold(candidate, ref) {
			return true
		}
	}
	return false
}

func vectorAssetRefs(item VectorAsset) []string {
	values := []string{item.Code}
	if relativePath := vectorRelativePath(item); relativePath != "" {
		values = append(values, relativePath)
	}
	if item.FileName != "" {
		values = append(values, strings.ReplaceAll(item.FileName, "\\", "/"))
	}
	return values
}

func vectorRelativePath(item VectorAsset) string {
	if item.Metadata == nil {
		return ""
	}
	value, ok := item.Metadata["relative_path"].(string)
	if !ok {
		return ""
	}
	cleaned, err := cleanVectorAssetRef(value)
	if err != nil {
		return ""
	}
	return cleaned
}

func vectorFormatAllowed(format string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, value := range allowed {
		if strings.EqualFold(strings.TrimSpace(value), format) {
			return true
		}
	}
	return false
}

func WriteVectorZip(w http.ResponseWriter, descriptor VectorDownloadDescriptor) error {
	archive := zip.NewWriter(w)
	for _, path := range descriptor.PackageFiles {
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		info, err := file.Stat()
		if err != nil {
			file.Close()
			return err
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			file.Close()
			return err
		}
		header.Name = filepath.Base(path)
		header.Method = zip.Deflate
		writer, err := archive.CreateHeader(header)
		if err != nil {
			file.Close()
			return err
		}
		if _, err := io.Copy(writer, file); err != nil {
			file.Close()
			return err
		}
		file.Close()
	}
	return archive.Close()
}
