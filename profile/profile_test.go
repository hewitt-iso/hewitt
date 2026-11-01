package profile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPersistenceAndIsolation(t *testing.T) {
	root := t.TempDir()
	first, err := Prepare(root, "codex", "personal")
	if err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(first, "codex", "synthetic-state")
	if err := os.WriteFile(state, []byte("native state"), 0o600); err != nil {
		t.Fatal(err)
	}
	again, err := Prepare(root, "codex", "personal")
	if err != nil || again != first {
		t.Fatalf("repeat = %q, %v", again, err)
	}
	if data, err := os.ReadFile(state); err != nil || string(data) != "native state" {
		t.Fatalf("state lost: %q, %v", data, err)
	}
	for _, pair := range [][2]string{{"claude", "personal"}, {"codex", "work"}} {
		other, err := Prepare(root, pair[0], pair[1])
		if err != nil || other == first {
			t.Fatalf("profile collision: %q, %v", other, err)
		}
		if _, err := os.Stat(filepath.Join(other, "codex", "synthetic-state")); !os.IsNotExist(err) {
			t.Fatalf("state leaked: %v", err)
		}
	}
	info, err := os.Stat(first)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("permissions: %v, %v", info, err)
	}
}

func TestInvalidIDsHaveNoSideEffects(t *testing.T) {
	for _, id := range []string{"", ".", "..", "../outside", "/tmp/x", "a/b", "a,b", "--x", "a\n"} {
		root := filepath.Join(t.TempDir(), "not-created")
		if _, err := Prepare(root, "codex", id); err == nil {
			t.Errorf("accepted %q", id)
		}
		if _, err := os.Stat(root); !os.IsNotExist(err) {
			t.Errorf("invalid ID created root: %v", err)
		}
	}
}

func TestRejectSymlinksAndSharedDirectories(t *testing.T) {
	for _, component := range []string{"profiles", "profiles/codex", "profiles/codex/personal", "profiles/codex/personal/home"} {
		t.Run(component, func(t *testing.T) {
			root, outside := t.TempDir(), t.TempDir()
			link := filepath.Join(root, component)
			if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, link); err != nil {
				t.Fatal(err)
			}
			if _, err := Prepare(root, "codex", "personal"); err == nil {
				t.Fatal("accepted symlink")
			}
			entries, err := os.ReadDir(outside)
			if err != nil || len(entries) != 0 {
				t.Fatalf("wrote through symlink: %v, %v", entries, err)
			}
		})
	}
	root := t.TempDir()
	p := filepath.Join(root, "profiles")
	if err := os.Mkdir(p, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Prepare(root, "claude", "default"); err == nil {
		t.Fatal("accepted shared directory")
	}
}
