package basemap

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func LoadManifest(path string) (Manifest, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}

	var item Manifest
	if err := json.Unmarshal(content, &item); err != nil {
		return Manifest{}, fmt.Errorf("decode manifest %s: %w", path, err)
	}

	item.Code = normalizeCode(item.Code)
	item.Name = strings.TrimSpace(item.Name)
	item.Type = defaultText(strings.TrimSpace(item.Type), "xyz")
	item.Description = strings.TrimSpace(item.Description)
	item.Version = strings.TrimSpace(item.Version)
	item.Status = defaultText(strings.TrimSpace(item.Status), "ready")
	item.TileFormat = normalizeTileFormat(item.TileFormat)
	item.TileScheme = defaultText(strings.TrimSpace(item.TileScheme), "xyz")
	item.Attribution = strings.TrimSpace(item.Attribution)
	item.RootPath = strings.TrimSpace(item.RootPath)
	if item.RootPath == "" {
		item.RootPath = "tiles"
	}
	if item.MaxZoom < item.MinZoom {
		return Manifest{}, fmt.Errorf("manifest %s has invalid zoom range", path)
	}
	if len(item.BBox) != 0 && len(item.BBox) != 4 {
		return Manifest{}, fmt.Errorf("manifest %s has invalid bbox", path)
	}
	if item.Code == "" {
		return Manifest{}, fmt.Errorf("manifest %s missing code", path)
	}
	if item.Name == "" {
		item.Name = item.Code
	}
	if item.Version == "" {
		item.Version = filepath.Base(filepath.Dir(path))
	}
	if item.Metadata == nil {
		item.Metadata = map[string]any{}
	}
	return item, nil
}

func normalizeCode(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return ""
	}
	var builder strings.Builder
	lastDash := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			builder.WriteRune(r)
			lastDash = false
		case r >= '0' && r <= '9':
			builder.WriteRune(r)
			lastDash = false
		case r == '_' || r == '-' || r == '.' || r == ' ':
			if builder.Len() > 0 && !lastDash {
				builder.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(builder.String(), "-")
}

func normalizeTileFormat(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	switch value {
	case "jpg", "jpeg":
		return "jpg"
	case "png", "webp", "pbf":
		return value
	case "":
		return "png"
	default:
		return value
	}
}
