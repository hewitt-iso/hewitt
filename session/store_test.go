package session

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func testMetadata() Metadata {
	return Metadata{
		ID: strings.Repeat("a", 64), Agent: "codex", Image: "codex:local",
		Name: "hewitt-test", CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
}

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestStoreRoundTrip(t *testing.T) {
	s := testStore(t)
	m := testMetadata()
	if err := s.Save(m); err != nil {
		t.Fatal(err)
	}
	// Reopening proves persistence does not depend on in-memory state.
	reopened, err := NewStore(s.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reopened.Get(m.ID)
	if err != nil || !reflect.DeepEqual(got, m) {
		t.Fatalf("Get = %+v, %v; want %+v", got, err, m)
	}
	for path, mode := range map[string]os.FileMode{
		s.dataDir:                            0o700,
		filepath.Join(s.dataDir, "sessions"): 0o700,
		filepath.Join(s.dataDir, "sessions", m.ID+".json"): 0o600,
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != mode {
			t.Errorf("%s permissions = %o, want %o", path, info.Mode().Perm(), mode)
		}
	}
	m.Name = "updated"
	if err := reopened.Save(m); err != nil {
		t.Fatal(err)
	}
	second := m
	second.ID = strings.Repeat("b", 64)
	if err := s.Save(second); err != nil {
		t.Fatal(err)
	}
	// Unrelated files and interrupted temporary writes are not records.
	if err := os.WriteFile(filepath.Join(s.dataDir, "sessions", ".tmp-abandoned"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	list, err := s.List()
	if err != nil || !reflect.DeepEqual(list, []Metadata{m, second}) {
		t.Fatalf("List = %+v, %v", list, err)
	}
	if err := s.Delete(m.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(m.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Get deleted = %v", err)
	}
	if err := s.Delete(m.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Delete missing = %v", err)
	}
	if _, err := s.Get(second.ID); err != nil {
		t.Fatalf("Delete affected other record: %v", err)
	}
}

func TestMissingStoreDoesNotCreateFiles(t *testing.T) {
	s := testStore(t)
	list, err := s.List()
	if err != nil || len(list) != 0 {
		t.Fatalf("List = %v, %v", list, err)
	}
	if _, err := s.Get(testMetadata().ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Get missing = %v", err)
	}
	if err := s.Delete(testMetadata().ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Delete missing = %v", err)
	}
	if _, err := os.Stat(s.dataDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only operations created data directory: %v", err)
	}
}

func TestStoreValidation(t *testing.T) {
	for _, dir := range []string{"", ".", "relative", "~/data"} {
		if _, err := NewStore(dir); err == nil {
			t.Errorf("NewStore(%q) succeeded", dir)
		}
	}
	s := testStore(t)
	for _, id := range []string{"", ".", "..", "../outside", "/tmp/outside", strings.Repeat("A", 64), strings.Repeat("g", 64), strings.Repeat("a", 63)} {
		m := testMetadata()
		m.ID = id
		if err := s.Save(m); err == nil {
			t.Errorf("Save accepted ID %q", id)
		}
		if _, err := s.Get(id); err == nil {
			t.Errorf("Get accepted ID %q", id)
		}
		if err := s.Delete(id); err == nil {
			t.Errorf("Delete accepted ID %q", id)
		}
	}
	for _, change := range []func(*Metadata){
		func(m *Metadata) { m.Profile = "../escape" },
		func(m *Metadata) { m.Operation = "unknown" },
		func(m *Metadata) { m.Agent = "unknown" },
		func(m *Metadata) { m.Image = "" },
		func(m *Metadata) { m.Image = "bad\nimage" },
		func(m *Metadata) { m.Name = "bad\tname" },
		func(m *Metadata) { m.CreatedAt = time.Time{} },
	} {
		m := testMetadata()
		change(&m)
		if err := s.Save(m); err == nil {
			t.Errorf("Save accepted invalid metadata: %+v", m)
		}
	}
	if _, err := os.Stat(s.dataDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid writes created data directory: %v", err)
	}
}

func TestMalformedRecords(t *testing.T) {
	valid := record{Version: 1, Metadata: testMetadata()}
	encode := func(r record) string {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	future := valid
	future.Version = 2
	mismatch := valid
	mismatch.ID = strings.Repeat("b", 64)
	invalid := valid
	invalid.Agent = ""
	for _, body := range []string{"{", "null", "[]", "{}", encode(valid) + "{}", encode(future), encode(mismatch), encode(invalid)} {
		t.Run(body, func(t *testing.T) {
			s := testStore(t)
			if err := s.Save(valid.Metadata); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(s.dataDir, "sessions", valid.ID+".json"), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Get(valid.ID); err == nil {
				t.Fatal("Get accepted corrupt record")
			}
			if got, err := s.List(); err == nil || len(got) != 0 {
				t.Fatalf("List hid corruption: %v, %v", got, err)
			}
		})
	}
}

func TestStoreSymlinkConfinement(t *testing.T) {
	s := testStore(t)
	m := testMetadata()
	if err := s.Save(m); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.dataDir, "sessions", m.ID+".json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(m.ID); err == nil {
		t.Fatal("Get followed symlink outside store")
	}
	if err := s.Save(m); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(outside); err != nil || string(got) != "untouched" {
		t.Fatalf("Save changed symlink target: %q, %v", got, err)
	}
	if err := s.Delete(m.ID); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(outside), filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(m); err == nil {
		t.Fatal("Save followed sessions directory outside data directory")
	}
}

func TestFailedSaveCleansTemporaryFile(t *testing.T) {
	s := testStore(t)
	m := testMetadata()
	path := filepath.Join(s.dataDir, "sessions", m.ID+".json")
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(m); err == nil {
		t.Fatal("Save over a directory succeeded")
	}
	if err := s.Delete(m.ID); err == nil {
		t.Fatal("Delete removed a directory instead of a metadata file")
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 || entries[0].Name() != m.ID+".json" {
		t.Fatalf("failed Save left temporary files: %v, %v", entries, err)
	}
}

func TestInvalidUpdatePreservesRecord(t *testing.T) {
	s := testStore(t)
	m := testMetadata()
	if err := s.Save(m); err != nil {
		t.Fatal(err)
	}
	invalid := m
	invalid.Agent = "unknown"
	if err := s.Save(invalid); err == nil {
		t.Fatal("Save accepted invalid update")
	}
	invalid = m
	invalid.CreatedAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.Save(invalid); err == nil {
		t.Fatal("Save accepted a time that JSON cannot encode")
	}
	if got, err := s.Get(m.ID); err != nil || !reflect.DeepEqual(got, m) {
		t.Fatalf("failed update changed record: %+v, %v", got, err)
	}
}

func TestConcurrentSaveAndRead(t *testing.T) {
	s := testStore(t)
	m := testMetadata()
	if err := s.Save(m); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 20 {
				if err := s.Save(m); err != nil {
					t.Error(err)
					return
				}
				if got, err := s.Get(m.ID); err != nil || !reflect.DeepEqual(got, m) {
					t.Errorf("Get during Save = %+v, %v", got, err)
					return
				}
				if _, err := s.List(); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	wg.Wait()
}

func TestTerminalMetadataCompatibility(t *testing.T) {
	store := testStore(t)
	m := testMetadata()
	for _, terminal := range []string{"", "tmux"} {
		m.Terminal = terminal
		if err := store.Save(m); err != nil {
			t.Fatal(err)
		}
		reopened, err := NewStore(store.dataDir)
		if err != nil {
			t.Fatal(err)
		}
		if got, err := reopened.Get(m.ID); err != nil || got.Terminal != terminal {
			t.Fatalf("terminal = %q, %v", got.Terminal, err)
		}
	}
	m.Terminal = "unknown"
	if err := store.Save(m); err == nil {
		t.Fatal("accepted unsupported terminal")
	}
}

func TestStoreResolve(t *testing.T) {
	store := testStore(t)
	first := testMetadata()
	first.ID = "0123456789abcdef" + strings.Repeat("a", 48)
	first.Name = "codex-work"
	second := testMetadata()
	second.ID = "0123456789abcdee" + strings.Repeat("b", 48)
	second.Name = "claude-work"
	for _, m := range []Metadata{first, second} {
		if err := store.Save(m); err != nil {
			t.Fatal(err)
		}
	}
	for _, ref := range []string{first.ID, first.ID[:16], "codex-work"} {
		got, err := store.Resolve(ref)
		if err != nil || got.ID != first.ID {
			t.Fatalf("Resolve(%q) = %+v, %v; want %s", ref, got, err, first.ID)
		}
	}
	if _, err := store.Resolve("0123456789abcde"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("Resolve ambiguous prefix = %v", err)
	}
	for _, ref := range []string{"missing", "deadbeef", strings.Repeat("c", 64)} {
		if _, err := store.Resolve(ref); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("Resolve(%q) = %v; want os.ErrNotExist", ref, err)
		}
	}
}
