package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func repository(t *testing.T) string {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	dir := t.TempDir()
	ctx := context.Background()
	for _, args := range [][]string{{"init"}, {"-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "initial"}} {
		if _, err := git(ctx, dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestWorktreeLifecycle(t *testing.T) {
	ctx := context.Background()
	repo, data := repository(t), t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "source-only"), []byte("dirty source"), 0600); err != nil {
		t.Fatal(err)
	}
	w, err := Create(ctx, data, repo, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	other, err := Create(ctx, data, w.Path, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if w.Path == other.Path || w.Branch == other.Branch || w.GitDir != other.GitDir {
		t.Fatalf("worktrees not isolated: %+v %+v", w, other)
	}
	if _, err := os.Stat(filepath.Join(w.Path, "source-only")); !os.IsNotExist(err) {
		t.Fatalf("copied source changes: %v", err)
	}
	for _, file := range []string{"untracked", ".gitignore", "ignored"} {
		content := "change"
		if file == ".gitignore" {
			content = "ignored\n"
		}
		if err := os.WriteFile(filepath.Join(w.Path, file), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := Remove(ctx, w); err == nil {
		t.Fatal("removed dirty worktree")
	}
	if _, err := git(ctx, w.Path, "add", "--all"); err != nil {
		t.Fatal(err)
	}
	if err := Remove(ctx, w); err == nil {
		t.Fatal("removed staged changes")
	}
	if _, err := git(ctx, w.Path, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "work"); err != nil {
		t.Fatal(err)
	}
	if err := Remove(ctx, w); err == nil {
		t.Fatal("removed ignored file")
	}
	if err := os.Remove(filepath.Join(w.Path, "ignored")); err != nil {
		t.Fatal(err)
	}
	if err := Remove(ctx, w); err != nil {
		t.Fatal(err)
	}
	if _, err := git(ctx, repo, "rev-parse", "--verify", w.Branch); err != nil {
		t.Fatal("removed committed branch:", err)
	}
	if err := Remove(ctx, other); err != nil {
		t.Fatal(err)
	}
}

func TestWorktreeFailures(t *testing.T) {
	repo, data := repository(t), filepath.Join(t.TempDir(), "absent")
	for _, base := range []string{"missing-ref", "--help"} {
		if _, err := Create(context.Background(), data, repo, base); err == nil {
			t.Fatal("accepted invalid base")
		}
	}
	if _, err := os.Stat(data); !os.IsNotExist(err) {
		t.Fatalf("invalid base created directories: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Create(ctx, data, repo, ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	if err := Remove(context.Background(), Worktree{Repository: repo, GitDir: filepath.Join(repo, ".git"), Path: repo, Branch: "hewitt/test"}); err == nil {
		t.Fatal("removed main checkout")
	}
}

func TestBaseAndModifiedTrackedFiles(t *testing.T) {
	ctx := context.Background()
	repo := repository(t)
	file := filepath.Join(repo, "tracked")
	if err := os.WriteFile(file, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "tracked"}, {"-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "second"}} {
		if _, err := git(ctx, repo, args...); err != nil {
			t.Fatal(err)
		}
	}
	old, err := Create(ctx, t.TempDir(), repo, "HEAD~1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(old.Path, "tracked")); !os.IsNotExist(err) {
		t.Fatalf("wrong base: %v", err)
	}
	w, err := Create(ctx, t.TempDir(), repo, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.Path, "tracked"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Remove(ctx, w); err == nil {
		t.Fatal("removed modified tracked file")
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "original" {
		t.Fatalf("source changed: %q %v", data, err)
	}
}
