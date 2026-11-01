package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".hewitt"); cfg.DataDir != want {
		t.Fatalf("DataDir = %q, want %q", cfg.DataDir, want)
	}
	if _, err := os.Stat(cfg.DataDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("loading defaults must not create data directory: %v", err)
	}

	dir, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "hewitt", "config.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"data_dir":"/tmp/hewitt-test-data"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load("")
	if err != nil || cfg.DataDir != "/tmp/hewitt-test-data" {
		t.Fatalf("Load default file = %+v, %v", cfg, err)
	}
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(""); err == nil {
		t.Fatal("malformed default config must fail")
	}
}

func TestLoadExplicit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, tt := range []struct {
		name    string
		body    string
		want    string
		wantErr string
	}{
		{name: "empty object", body: `{}`, want: filepath.Join(home, ".hewitt")},
		{name: "custom directory", body: `{"data_dir":"/tmp/hewitt data"}`, want: "/tmp/hewitt data"},
		{name: "invalid JSON", body: `{`, wantErr: "parse config"},
		{name: "unknown field", body: `{"data_dr":"/tmp/data"}`, wantErr: "unknown field"},
		{name: "relative path", body: `{"data_dir":"data"}`, wantErr: "absolute path"},
		{name: "empty path", body: `{"data_dir":""}`, wantErr: "absolute path"},
		{name: "tilde path", body: `{"data_dir":"~/data"}`, wantErr: "absolute path"},
		{name: "wrong type", body: `{"data_dir":42}`, wantErr: "decode data_dir"},
		{name: "null field", body: `{"data_dir":null}`, wantErr: "must be a string"},
		{name: "null document", body: `null`, wantErr: "JSON object"},
		{name: "array document", body: `[]`, wantErr: "parse config"},
		{name: "empty file", body: ``, wantErr: "parse config"},
		{name: "trailing document", body: `{} {}`, wantErr: "parse config"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(tt.body), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(path)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.DataDir != tt.want {
				t.Errorf("DataDir = %q, want %q", cfg.DataDir, tt.want)
			}
		})
	}
}

func TestLoadMissingExplicitFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, err := Load(filepath.Join(t.TempDir(), "missing.json"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("error = %v, want wrapped os.ErrNotExist", err)
	}
}
