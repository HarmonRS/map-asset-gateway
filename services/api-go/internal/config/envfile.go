package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func LoadEnvDefaults() error {
	if explicit := strings.TrimSpace(os.Getenv("APP_ENV_FILE")); explicit != "" {
		if err := loadEnvFile(explicit); err != nil {
			return fmt.Errorf("load APP_ENV_FILE %s: %w", explicit, err)
		}
		return nil
	}

	for _, candidate := range []string{
		filepath.Join("config", "service.env"),
		filepath.Join("..", "..", "config", "service.env"),
	} {
		if _, err := os.Stat(candidate); err == nil {
			if err := loadEnvFile(candidate); err != nil {
				return fmt.Errorf("load env file %s: %w", candidate, err)
			}
			return nil
		}
	}
	return nil
}

func loadEnvFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}

		key, value, found := strings.Cut(line, "=")
		if !found {
			return fmt.Errorf("line %d: missing '='", lineNo)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		if key == "" {
			return fmt.Errorf("line %d: empty key", lineNo)
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("line %d: set %s: %w", lineNo, key, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}
