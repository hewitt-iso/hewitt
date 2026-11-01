package cmd

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hewitt-iso/hewitt/config"
	"github.com/hewitt-iso/hewitt/session"
)

func TestPS(t *testing.T) {
	for _, customDir := range []bool{false, true} {
		t.Run(map[bool]string{false: "default data directory", true: "custom data directory"}[customDir], func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
			t.Setenv("PATH", home) // Listing saved sessions must work without Docker.
			dataDir := filepath.Join(home, ".hewitt")
			args := []string{"ps"}
			if customDir {
				dataDir = filepath.Join(home, "custom-data")
				data, err := json.Marshal(config.Config{DataDir: dataDir})
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(home, "config.json")
				if err := os.WriteFile(path, data, 0o600); err != nil {
					t.Fatal(err)
				}
				args = append(args, "--config", path)
			}
			out, err := executeCommand(t, args...)
			if err != nil || strings.Join(strings.Fields(out), " ") != "ID AGENT IMAGE NAME CREATED" || strings.Count(out, "\n") != 1 {
				t.Fatalf("empty list = %q, %v", out, err)
			}
			if _, err := os.Stat(dataDir); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("listing created data directory: %v", err)
			}
			store, err := session.NewStore(dataDir)
			if err != nil {
				t.Fatal(err)
			}
			first := session.Metadata{
				ID: containerID, Agent: "codex", Image: "my-codex:local",
				CreatedAt: time.Date(2026, 11, 1, 14, 0, 0, 0, time.FixedZone("UTC+2", 7200)),
			}
			second := session.Metadata{
				ID: strings.Repeat("b", 64), Agent: "claude", Image: "my-claude:local",
				Name: "hewitt-claude", CreatedAt: first.CreatedAt.Add(-time.Hour),
			}
			for _, m := range []session.Metadata{second, first} {
				if err := store.Save(m); err != nil {
					t.Fatal(err)
				}
			}
			out, err = executeCommand(t, args...)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
			want := []string{
				"ID AGENT IMAGE NAME CREATED",
				containerID + " codex my-codex:local - 2026-11-01T12:00:00Z",
				second.ID + " claude my-claude:local hewitt-claude 2026-11-01T11:00:00Z",
			}
			for i := range lines {
				lines[i] = strings.Join(strings.Fields(lines[i]), " ")
			}
			if !reflect.DeepEqual(lines, want) {
				t.Fatalf("list = %q, want %q", lines, want)
			}
			for _, m := range []session.Metadata{first, second} {
				got, err := store.Get(m.ID)
				if err != nil || got.ID != m.ID || got.Name != m.Name || !got.CreatedAt.Equal(m.CreatedAt) {
					t.Fatalf("listing changed metadata: %+v, %v", got, err)
				}
			}
			// A corrupt later record must fail without printing a partial list.
			if err := os.WriteFile(filepath.Join(dataDir, "sessions", second.ID+".json"), []byte("{"), 0o600); err != nil {
				t.Fatal(err)
			}
			out, err = executeCommand(t, args...)
			if err == nil || !strings.Contains(err.Error(), "list sessions") || !strings.Contains(err.Error(), second.ID) || out != "" {
				t.Fatalf("corrupt store: output = %q, error = %v", out, err)
			}
		})
	}
}

func TestPSValidationAndHelp(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("PATH", home)
	missing := filepath.Join(home, "missing.json")
	for _, args := range [][]string{
		{"ps", "--help", "--config", missing},
		{"--config", missing, "help", "ps"},
	} {
		out, err := executeCommand(t, args...)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"hewitt ps", "Examples:", "--config", "Docker is not required or queried"} {
			if !strings.Contains(out, want) {
				t.Errorf("help = %q, want %q", out, want)
			}
		}
		if strings.Contains(out, "not implemented") {
			t.Errorf("help still describes ps as unimplemented: %q", out)
		}
	}
	for _, args := range [][]string{{"ps", "extra", "--config", missing}, {"ps", "--unknown"}} {
		out, err := executeCommand(t, args...)
		if err == nil || strings.Contains(err.Error(), "config") || out != "" {
			t.Fatalf("%v: output = %q, error = %v; want argument/flag error before config loading", args, out, err)
		}
	}
}

func TestPSStoreError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("PATH", home)
	if err := os.WriteFile(filepath.Join(home, ".hewitt"), []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := executeCommand(t, "ps")
	if err == nil || !strings.Contains(err.Error(), "list sessions") || out != "" {
		t.Fatalf("output = %q, error = %v; want store error without output", out, err)
	}
}

type failingPSWriter struct{ err error }

func (w failingPSWriter) Write(p []byte) (int, error) { return 0, w.err }

func TestPSOutputError(t *testing.T) {
	want := errors.New("output unavailable")
	cmd := newPSCommand(&config.Config{DataDir: filepath.Join(t.TempDir(), "data")})
	cmd.SetOut(failingPSWriter{err: want})
	if err := cmd.RunE(cmd, nil); !errors.Is(err, want) {
		t.Fatalf("output error = %v, want %v", err, want)
	}
}
