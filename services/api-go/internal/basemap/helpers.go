package basemap

func defaultText(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
