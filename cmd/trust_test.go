package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hewitt-iso/hewitt/config"
)

func TestTrustCommand(t *testing.T) {
	runRepository(t)
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	t.Setenv("PATH", dir)
	cfg, err := json.Marshal(config.Config{DataDir: filepath.Join(dir, "data")})
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, cfg, 0o600); err != nil {
		t.Fatal(err)
	}
	repo, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(repo, "hewitt.yml")
	if err := os.WriteFile(file, []byte("image: someone/else:latest\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run := []string{"run", "codex", "--detach", "--config", configPath}
	if _, err := executeCommand(t, run...); err == nil || !strings.Contains(err.Error(), "is not trusted and sets image") {
		t.Fatalf("untrusted run = %v", err)
	}
	out, err := executeCommand(t, "trust", "--config", configPath)
	if err != nil || out != "Trusted "+file+": image\n" {
		t.Fatalf("trust = %q, %v", out, err)
	}
	if _, err := executeCommand(t, run...); err == nil || strings.Contains(err.Error(), "trusted") {
		t.Fatalf("trusted run = %v", err)
	}
	if err := os.WriteFile(file, []byte("image: someone/else:v2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := executeCommand(t, run...); err == nil || !strings.Contains(err.Error(), "changed since it was trusted") {
		t.Fatalf("changed file = %v", err)
	}
}
