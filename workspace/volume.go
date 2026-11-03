package workspace

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Volume is a session repository kept in a Docker volume. The container owns
// its .git, hooks, and config. The host repository is only read, through a
// read-only mount of its object directory used as an alternate, and receives
// branches from bundles, so host Git never reads the container's repository.
type Volume struct {
	// Name is the session name; it names the volume and the host branch namespace.
	Name string `json:"name"`
	// Volume is the Docker volume holding the repository and its worktrees.
	Volume     string `json:"volume"`
	Repository string `json:"repository"`
	// Objects is the host object directory mounted read-only as the alternate.
	Objects string `json:"objects"`
	// Alternates are further object directories the host repository borrows.
	Alternates []string `json:"alternates,omitempty"`
	Base       string   `json:"base"`
	// Path is the repository inside the volume.
	Path string `json:"path"`
	// Branch is the branch checked out first inside the volume.
	Branch string `json:"branch"`
	// Origin is the host origin URL without user information, if any.
	Origin string `json:"origin,omitempty"`
	// UserName and UserEmail are the host repository's commit identity.
	UserName  string `json:"user_name,omitempty"`
	UserEmail string `json:"user_email,omitempty"`
}

var sessionName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)

// ValidName reports whether name can name a session volume and its branches.
func ValidName(name string) bool {
	return sessionName.MatchString(name) && !strings.Contains(name, "..") && !strings.HasSuffix(name, ".lock") && !strings.HasSuffix(name, ".")
}

// Prefix is the host branch namespace for imported branches.
func (v Volume) Prefix() string { return "hewitt/" + v.Name }

// anchor keeps the base commit reachable on the host, so gc never removes
// objects the volume borrows through its alternate.
func (v Volume) anchor() string { return "refs/hewitt/" + v.Name + "/base" }

// PrepareVolume resolves the host repository and base (HEAD when empty) and
// anchors the base. Nothing is created in Docker. The first branch keeps the
// host branch name when base is a local branch or HEAD on one, otherwise "work".
func PrepareVolume(ctx context.Context, repository, base, name string) (Volume, error) {
	if !ValidName(name) {
		return Volume{}, fmt.Errorf("session name %q must be lowercase letters, digits, '.', '_' or '-'", name)
	}
	repo, err := Toplevel(ctx, repository)
	if err != nil {
		return Volume{}, err
	}
	common, err := git(ctx, repo, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return Volume{}, err
	}
	if common, err = filepath.EvalSymlinks(common); err != nil {
		return Volume{}, fmt.Errorf("resolve Git directory: %w", err)
	}
	if base == "" {
		base = "HEAD"
	}
	commit, err := git(ctx, repo, "rev-parse", "--verify", "--end-of-options", base+"^{commit}")
	if err != nil {
		return Volume{}, fmt.Errorf("resolve workspace base: %w", err)
	}
	branch := "work"
	ref, err := git(ctx, repo, "rev-parse", "--symbolic-full-name", "--end-of-options", base)
	if base == "HEAD" {
		ref, err = git(ctx, repo, "symbolic-ref", "-q", "HEAD")
	}
	if short, ok := strings.CutPrefix(ref, "refs/heads/"); ok && err == nil {
		branch = short
	}
	v := Volume{Name: name, Volume: "hewitt-" + name, Repository: repo, Objects: filepath.Join(common, "objects"), Base: commit, Branch: branch}
	if v.Alternates, err = alternates(v.Objects); err != nil {
		return Volume{}, err
	}
	if taken, err := NameTaken(ctx, repo, name); err != nil {
		return Volume{}, err
	} else if taken {
		return Volume{}, fmt.Errorf("session name %q is in use: refs/heads/%s or its anchor exists; choose another --name", name, v.Prefix())
	}
	if origin, err := git(ctx, repo, "remote", "get-url", "origin"); err == nil {
		v.Origin = stripUserinfo(origin)
	}
	v.UserName, _ = git(ctx, repo, "config", "user.name")
	v.UserEmail, _ = git(ctx, repo, "config", "user.email")
	if _, err := git(ctx, repo, "update-ref", "--", v.anchor(), commit, ""); err != nil {
		return Volume{}, fmt.Errorf("anchor workspace base: %w", err)
	}
	return v, nil
}

// Validate checks a persisted record before its paths become mounts or Git arguments.
func (v Volume) Validate() error {
	if !ValidName(v.Name) || v.Volume != "hewitt-"+v.Name {
		return fmt.Errorf("invalid session volume name %q", v.Name)
	}
	for _, path := range append([]string{v.Repository, v.Objects, v.Path}, v.Alternates...) {
		if !filepath.IsAbs(path) || strings.ContainsAny(path, ",\x00\r\n") {
			return fmt.Errorf("session volume paths must be absolute and contain no commas or control characters")
		}
	}
	if len(v.Base) < 40 || strings.Trim(v.Base, "0123456789abcdef") != "" || v.Branch == "" || strings.HasPrefix(v.Branch, "-") {
		return fmt.Errorf("invalid session volume base or branch")
	}
	return nil
}

// Toplevel returns the root of the repository containing dir.
func Toplevel(ctx context.Context, dir string) (string, error) {
	if dir == "" {
		dir = "."
	}
	repo, err := git(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("locate repository: %w", err)
	}
	return repo, nil
}

// NameTaken reports whether repository has branches or an anchor for name.
func NameTaken(ctx context.Context, repository, name string) (bool, error) {
	v := Volume{Name: name}
	taken, err := git(ctx, repository, "for-each-ref", "--count=1", "--format=%(refname)", "refs/heads/"+v.Prefix(), v.anchor())
	if err != nil {
		return false, err
	}
	return taken != "", nil
}

// alternates returns absolute object directories the host repository borrows;
// they must be mounted at the same paths for the borrowed objects to resolve.
func alternates(objects string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(objects, "info", "alternates"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read Git alternates: %w", err)
	}
	var dirs []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !filepath.IsAbs(line) {
			line = filepath.Join(objects, line)
		}
		real, err := filepath.EvalSymlinks(line)
		if err != nil {
			return nil, fmt.Errorf("resolve Git alternate %q: %w", line, err)
		}
		dirs = append(dirs, real)
	}
	return dirs, nil
}

// stripUserinfo drops credentials embedded in an HTTP(S) remote URL; they must
// not reach the container. SSH and scp-style URLs are returned unchanged.
func stripUserinfo(remote string) string {
	u, err := url.Parse(remote)
	if err != nil || u.User == nil || (u.Scheme != "http" && u.Scheme != "https") {
		return remote
	}
	u.User = nil
	return u.String()
}

// Import fetches every branch of a bundle into the host namespace
// hewitt/<name>/<branch>. Imported branches are overwritten by newer bundles.
// It returns the updated host branches. Fetch runs no hooks or configuration
// from the bundle, which carries only objects and refs.
func (v Volume) Import(ctx context.Context, bundle string) ([]string, error) {
	before, err := v.Branches(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := git(ctx, v.Repository, "fetch", "--quiet", "--no-tags", "--no-write-fetch-head", "--end-of-options",
		bundle, "+refs/heads/*:refs/heads/"+v.Prefix()+"/*"); err != nil {
		return nil, fmt.Errorf("import session branches: %w", err)
	}
	after, err := v.Branches(ctx)
	if err != nil {
		return nil, err
	}
	var changed []string
	for ref, commit := range after {
		if before[ref] != commit {
			changed = append(changed, ref)
		}
	}
	return changed, nil
}

// Branches maps imported host branches to their commits.
func (v Volume) Branches(ctx context.Context) (map[string]string, error) {
	out, err := git(ctx, v.Repository, "for-each-ref", "--format=%(refname:short) %(objectname)", "refs/heads/"+v.Prefix()+"/")
	if err != nil {
		return nil, err
	}
	branches := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if ref, commit, ok := strings.Cut(line, " "); ok {
			branches[ref] = commit
		}
	}
	return branches, nil
}

// Release deletes the base anchor. Imported branches are kept.
func (v Volume) Release(ctx context.Context) error {
	if _, err := git(ctx, v.Repository, "update-ref", "-d", "--", v.anchor()); err != nil {
		return fmt.Errorf("release workspace base: %w", err)
	}
	return nil
}
