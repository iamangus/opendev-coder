package config

import (
	"os"
	"path/filepath"
	"testing"
)

// writeConfig is a test helper that writes content to .opendev/config.yaml
// inside dir, creating the directory if needed.
func writeConfig(t *testing.T, dir, content string) {
	t.Helper()
	configDir := filepath.Join(dir, ".opendev")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("creating .opendev dir: %v", err)
	}
	path := filepath.Join(configDir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing config file: %v", err)
	}
}

func TestLoad_HappyPath(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "test_command: \"go test ./...\"\n")

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.TestCommand != "go test ./..." {
		t.Errorf("unexpected test_command: %q", cfg.TestCommand)
	}
}

func TestLoad_WhitespaceIsTrimmed(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "test_command: \"  go test ./...  \"\n")

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.TestCommand != "go test ./..." {
		t.Errorf("expected trimmed test_command, got %q", cfg.TestCommand)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	dir := t.TempDir()
	// No config file written.

	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected error for missing config file, got nil")
	}
}

func TestLoad_EmptyTestCommand(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "test_command: \"\"\n")

	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected error for empty test_command, got nil")
	}
}

func TestLoad_MissingTestCommandField(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "some_other_field: value\n")

	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected error when test_command field is absent, got nil")
	}
}

func TestLoad_MalformedYAML(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "test_command: [\nbad yaml")

	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected error for malformed YAML, got nil")
	}
}

func TestLoad_WhitespaceOnlyTestCommand(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "test_command: \"   \"\n")

	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected error for whitespace-only test_command, got nil")
	}
}
