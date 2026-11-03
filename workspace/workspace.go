// Package workspace creates and safely removes Git worktrees through the Git CLI.
package workspace

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Worktree identifies a linked checkout and its shared repository metadata.
// GitDir and Path are mounted at their host paths so Git's absolute links work.
type Worktree struct {
	Repository string `json:"repository"`
	GitDir     string `json:"git_dir"`
	Path       string `json:"path"`
	Branch     string `json:"branch"`
	// Base is the commit the branch started from; empty in older records.
	Base string `json:"base,omitempty"`
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSuffix(out.String(), "\n"), nil
}

// Create checks out a new branch at base (HEAD when empty). Source changes are
// never copied or stashed. On Git failure the intended location is returned for
// inspection; no automatic cleanup can discard partially created work.
func Create(ctx context.Context, dataDir, repository, base string) (Worktree, error) {
	if !filepath.IsAbs(dataDir) {
		return Worktree{}, fmt.Errorf("workspace data directory must be absolute")
	}
	if repository == "" {
		repository = "."
	}
	repo, err := git(ctx, repository, "rev-parse", "--show-toplevel")
	if err != nil {
		return Worktree{}, fmt.Errorf("locate repository: %w", err)
	}
	repo, err = filepath.EvalSymlinks(repo)
	if err != nil {
		return Worktree{}, fmt.Errorf("resolve repository: %w", err)
	}
	common, err := git(ctx, repo, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return Worktree{}, err
	}
	common, err = filepath.EvalSymlinks(common)
	if err != nil {
		return Worktree{}, fmt.Errorf("resolve Git directory: %w", err)
	}
	if base == "" {
		base = "HEAD"
	}
	commit, err := git(ctx, repo, "rev-parse", "--verify", "--end-of-options", base+"^{commit}")
	if err != nil {
		return Worktree{}, fmt.Errorf("resolve workspace base: %w", err)
	}
	root := filepath.Join(dataDir, "worktrees")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return Worktree{}, fmt.Errorf("create worktree directory: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return Worktree{}, err
	}
	token := strings.ToLower(rand.Text())
	w := Worktree{Repository: repo, GitDir: common, Path: filepath.Join(root, token), Branch: "hewitt/" + token, Base: commit}
	if err := w.Validate(); err != nil {
		return w, err
	}
	if _, err := git(ctx, repo, "worktree", "add", "-b", w.Branch, "--", w.Path, commit); err != nil {
		return w, fmt.Errorf("create worktree %q (inspect before cleanup): %w", w.Path, err)
	}
	return w, nil
}

// Validate checks persisted paths before they can become container mounts.
func (w Worktree) Validate() error {
	for _, path := range []string{w.Repository, w.GitDir, w.Path} {
		if !filepath.IsAbs(path) || strings.ContainsAny(path, ",\x00\r\n") {
			return fmt.Errorf("workspace paths must be absolute and contain no commas or control characters")
		}
	}
	if !strings.HasPrefix(w.Branch, "hewitt/") || len(w.Branch) <= len("hewitt/") {
		return fmt.Errorf("invalid workspace branch")
	}
	return nil
}

// Remove refuses modified, untracked, or ignored files and never forces removal.
// The branch remains available for recovery of committed work.
func Remove(ctx context.Context, w Worktree) error {
	if err := CheckRemovable(ctx, w); err != nil {
		return err
	}
	if _, err := git(ctx, w.GitDir, "worktree", "remove", "--", w.Path); err != nil {
		return fmt.Errorf("remove worktree: %w", err)
	}
	return nil
}

// CheckRemovable reports why Remove would refuse, without changing anything.
func CheckRemovable(ctx context.Context, w Worktree) error {
	if err := w.Validate(); err != nil {
		return err
	}
	common, err := git(ctx, w.Path, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return err
	}
	common, err = filepath.EvalSymlinks(common)
	if err != nil {
		return err
	}
	if common != w.GitDir {
		return fmt.Errorf("worktree repository does not match metadata")
	}
	top, err := git(ctx, w.Path, "rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	actual, err := filepath.EvalSymlinks(w.Path)
	if err != nil {
		return err
	}
	if top != actual || actual == w.Repository {
		return fmt.Errorf("refusing to remove a non-linked worktree")
	}
	status, err := git(ctx, w.Path, "status", "--porcelain", "--untracked-files=all", "--ignored")
	if err != nil {
		return err
	}
	if status != "" {
		return fmt.Errorf("worktree %q has uncommitted, untracked, or ignored files", w.Path)
	}
	return nil
}

// RemoveForce removes a worktree and any sibling worktrees created beneath its
// parent worktrees directory for the same repository, discarding uncommitted or
// untracked files while retaining branches.
func RemoveAll(ctx context.Context, w Worktree) error {
	if err := w.Validate(); err != nil {
		return err
	}
	parent := filepath.Dir(w.Path)
	entries, err := os.ReadDir(parent)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read worktrees directory: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		candidate := filepath.Join(parent, entry.Name())
		common, err := git(ctx, candidate, "rev-parse", "--path-format=absolute", "--git-common-dir")
		if err != nil {
			continue
		}
		if resolved, err := filepath.EvalSymlinks(common); err == nil && resolved == w.GitDir {
			if _, err := git(ctx, w.GitDir, "worktree", "remove", "--force", "--", candidate); err != nil {
				return fmt.Errorf("remove worktree %q: %w", candidate, err)
			}
		}
	}
	if _, err := os.Lstat(w.Path); err == nil {
		if _, err := git(ctx, w.GitDir, "worktree", "remove", "--force", "--", w.Path); err != nil {
			return fmt.Errorf("remove worktree: %w", err)
		}
	}
	_, _ = git(ctx, w.GitDir, "worktree", "prune")
	return nil
}
