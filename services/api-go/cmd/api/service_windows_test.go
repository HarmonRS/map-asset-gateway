//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRotatingFileWriterRollsOver(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "svc.log")

	writer, err := newRotatingFileWriter(path, 64)
	if err != nil {
		t.Fatalf("open writer: %v", err)
	}
	defer writer.Close()

	for i := 0; i < 10; i++ {
		if _, err := writer.Write([]byte(strings.Repeat("x", 20) + "\n")); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("current log missing: %v", err)
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("expected a rolled file at %s.1: %v", path, err)
	}
}

func TestSetupServiceLoggingUsesConfiguredPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "service.log")
	t.Setenv("SERVICE_LOG_FILE", path)

	restore := setupServiceLogging()
	defer restore()

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected the log file at %s: %v", path, err)
	}
}
