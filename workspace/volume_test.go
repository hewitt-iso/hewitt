package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareVolume(t *testing.T) {
	ctx := context.Background()
	repo := repository(t)
	for _, args := range [][]string{
		{"branch", "-m", "main"},
		{"remote", "add", "origin", "https://user:secret@example.com/team/repo.git"},
		{"config", "user.name", "Test"},
		{"config", "user.email", "test@example.com"},
	} {
		if _, err := git(ctx, repo, args...); err != nil {
			t.Fatal(err)
		}
	}
	v, err := PrepareVolume(ctx, repo, "", "claude-1")
	if err != nil {
		t.Fatal(err)
	}
	head, _ := git(ctx, repo, "rev-parse", "HEAD")
	if v.Volume != "hewitt-claude-1" || v.Branch != "main" || v.Base != head || v.Prefix() != "hewitt/claude-1" {
		t.Fatalf("volume = %+v", v)
	}
	if v.Origin != "https://example.com/team/repo.git" || strings.Contains(v.Origin, "secret") {
		t.Fatalf("origin keeps credentials: %q", v.Origin)
	}
	if v.UserName != "Test" || v.UserEmail != "test@example.com" {
		t.Fatalf("identity = %q <%q>", v.UserName, v.UserEmail)
	}
	if !strings.HasSuffix(v.Objects, filepath.Join(".git", "objects")) {
		t.Fatalf("objects = %q", v.Objects)
	}
	if anchored, err := git(ctx, repo, "rev-parse", "refs/hewitt/claude-1/base"); err != nil || anchored != head {
		t.Fatalf("anchor = %q, %v", anchored, err)
	}
	if _, err := PrepareVolume(ctx, repo, "", "claude-1"); err == nil {
		t.Fatal("reused a session name with an anchor")
	}
	detached, err := PrepareVolume(ctx, repo, head, "codex-1")
	if err != nil {
		t.Fatal(err)
	}
	if detached.Branch != "work" {
		t.Fatalf("commit base branch = %q, want work", detached.Branch)
	}
	if err := v.Release(ctx); err != nil {
		t.Fatal(err)
	}
	if taken, err := NameTaken(ctx, repo, "claude-1"); err != nil || taken {
		t.Fatalf("released name taken = %v, %v", taken, err)
	}
}

func TestVolumeNamesAndRecords(t *testing.T) {
	for _, name := range []string{"claude-1", "fix.login", "a_b"} {
		if !ValidName(name) {
			t.Errorf("%q rejected", name)
		}
	}
	for _, name := range []string{"", "-x", "Claude", "a b", "a..b", "x.lock", "x.", "a/b", strings.Repeat("a", 64)} {
		if ValidName(name) {
			t.Errorf("%q accepted", name)
		}
	}
	good := Volume{Name: "c-1", Volume: "hewitt-c-1", Repository: "/r", Objects: "/r/.git/objects", Path: "/hewitt/work/r", Base: strings.Repeat("a", 40), Branch: "main"}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Volume){
		func(v *Volume) { v.Volume = "other" },
		func(v *Volume) { v.Objects = "relative" },
		func(v *Volume) { v.Path = "/a,b" },
		func(v *Volume) { v.Base = "HEAD" },
		func(v *Volume) { v.Branch = "-x" },
		func(v *Volume) { v.Alternates = []string{"x"} },
	} {
		v := good
		change(&v)
		if v.Validate() == nil {
			t.Errorf("accepted %+v", v)
		}
	}
}

func TestStripUserinfo(t *testing.T) {
	for in, want := range map[string]string{
		"https://tok@github.com/a/b.git": "https://github.com/a/b.git",
		"git@github.com:a/b.git":         "git@github.com:a/b.git",
		"ssh://git@github.com/a/b.git":   "ssh://git@github.com/a/b.git",
		"http://u:p@example.com/x":       "http://example.com/x",
		"/local/path":                    "/local/path",
	} {
		if got := stripUserinfo(in); got != want {
			t.Errorf("stripUserinfo(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAlternatesResolved(t *testing.T) {
	objects := filepath.Join(t.TempDir(), "objects")
	borrowed := t.TempDir()
	if err := os.MkdirAll(filepath.Join(objects, "info"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(objects, "info", "alternates"), []byte("# comment\n"+borrowed+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirs, err := alternates(objects)
	if err != nil {
		t.Fatal(err)
	}
	real, _ := filepath.EvalSymlinks(borrowed)
	if len(dirs) != 1 || dirs[0] != real {
		t.Fatalf("alternates = %v", dirs)
	}
}
