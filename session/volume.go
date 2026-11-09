package session

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/hewitt-iso/hewitt/docker"
	"github.com/hewitt-iso/hewitt/workspace"
)

// Volume workspaces keep the session repository in a Docker volume mounted at
// volumeRoot. Hewitt drives it only through one-shot helper containers running
// the scripts below with direct arguments; host Git never opens it.
const (
	volumeRoot    = "/hewitt/work"
	sourceObjects = "/hewitt/source/objects"
)

const (
	// chownScript runs as root with only CAP_CHOWN: a new volume is root-owned
	// unless the image already has a writable directory at its mount point.
	chownScript = `chown "$1" "$2"`
	initScript  = `set -eu
repo=$1 branch=$2 base=$3
if [ -e "$repo/.git" ]; then exit 0; fi
git init -q -b "$branch" "$repo"
printf '%s\n' "$4" > "$repo/.git/objects/info/alternates"
git -C "$repo" update-ref "refs/heads/$branch" "$base"
git -C "$repo" reset -q --hard
if [ -n "$5" ]; then git -C "$repo" remote add origin "$5"; fi
if [ -n "$6" ]; then git -C "$repo" config user.name "$6"; fi
if [ -n "$7" ]; then git -C "$repo" config user.email "$7"; fi`
	// bundleScript writes nothing when no branch has commits past the base;
	// git refuses to create an empty bundle.
	bundleScript = `set -eu
cd "$1"
if [ -z "$(git rev-list -n 1 --branches --not "$2")" ]; then exit 0; fi
exec git bundle create --quiet - --branches --not "$2"`
	// dirtyScript lists worktrees with uncommitted or untracked files.
	dirtyScript = `set -eu
cd "$1"
git worktree list --porcelain | sed -n 's/^worktree //p' | while IFS= read -r wt; do
	if [ -n "$(git -C "$wt" status --porcelain --untracked-files=all)" ]; then printf '%s\n' "$wt"; fi
done`
)

// volumeRepo is the repository path inside the volume. The project name keeps
// per-project client state (keyed by working directory) apart.
func volumeRepo(project string) string {
	if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`).MatchString(project) {
		project = "repo"
	}
	return volumeRoot + "/" + project
}

// volumeMounts returns the volume and the read-only host objects it borrows.
func volumeMounts(v workspace.Volume) ([]docker.VolumeMount, []docker.BindMount) {
	binds := []docker.BindMount{{Source: v.Objects, Target: sourceObjects, ReadOnly: true}}
	for _, dir := range v.Alternates {
		binds = append(binds, docker.BindMount{Source: dir, Target: dir, ReadOnly: true})
	}
	return []docker.VolumeMount{{Name: v.Volume, Target: volumeRoot}}, binds
}

// helper runs a script in a network-less, read-only container as the invoking
// user, against the session volume.
func helper(ctx context.Context, image string, v workspace.Volume, script string, out *bytes.Buffer, args ...string) error {
	volumes, binds := volumeMounts(v)
	opts := docker.CreateOptions{
		Image: image, Entrypoint: "sh", Command: append([]string{"-c", script, "sh"}, args...),
		User: fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), Env: []string{"HOME=/tmp"},
		Volumes: volumes, Mounts: binds, Network: "none", ReadOnlyRoot: true, DisableLogs: true, DisableTTY: true,
	}
	if out == nil {
		out = &bytes.Buffer{}
	}
	return docker.Client{}.Run(ctx, opts, out)
}

// createVolume creates and initializes the session repository at the base.
// On failure the volume is kept and named so it can be inspected.
func createVolume(ctx context.Context, image string, v workspace.Volume, stderr io.Writer) error {
	client := docker.Client{}
	if err := reportStep(stderr, "Creating workspace volume"); err != nil {
		return err
	}
	if err := client.CreateVolume(ctx, v.Volume); err != nil {
		return err
	}
	volumes, _ := volumeMounts(v)
	owner := docker.CreateOptions{
		Image: image, Entrypoint: "sh", Command: []string{"-c", chownScript, "sh", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), volumeRoot},
		User: "0:0", CapAdd: []string{"CHOWN"}, Volumes: volumes, Network: "none", ReadOnlyRoot: true, DisableLogs: true, DisableTTY: true,
	}
	if err := reportStep(stderr, "Preparing volume permissions (pulling the image if needed)"); err != nil {
		return err
	}
	if err := client.Run(ctx, owner, &bytes.Buffer{}); err != nil {
		return fmt.Errorf("prepare volume %q (preserved): %w", v.Volume, err)
	}
	if err := reportStep(stderr, "Initializing workspace repository"); err != nil {
		return err
	}
	if err := helper(ctx, image, v, initScript, nil, v.Path, v.Branch, v.Base, sourceObjects, v.Origin, v.UserName, v.UserEmail); err != nil {
		return fmt.Errorf("initialize repository in volume %q (preserved): %w", v.Volume, err)
	}
	return nil
}

// Sync imports a session's branches into the host repository as
// hewitt/<name>/<branch>. It works on running and stopped sessions. It returns
// the host branches that changed.
func Sync(ctx context.Context, store *Store, ref string) ([]string, error) {
	m, err := store.Resolve(ref)
	if err != nil {
		return nil, err
	}
	if m.Volume == nil {
		return nil, fmt.Errorf("session %s has no volume workspace to sync", m.ID)
	}
	return syncVolume(ctx, m)
}

func syncVolume(ctx context.Context, m Metadata) ([]string, error) {
	v := *m.Volume
	var bundle bytes.Buffer
	if err := helper(ctx, m.Image, v, bundleScript, &bundle, v.Path, v.Base); err != nil {
		return nil, fmt.Errorf("export branches from volume %q: %w", v.Volume, err)
	}
	if bundle.Len() == 0 {
		return nil, nil
	}
	file, err := os.CreateTemp("", "hewitt-*.bundle")
	if err != nil {
		return nil, fmt.Errorf("store session bundle: %w", err)
	}
	defer os.Remove(file.Name())
	_, err = file.Write(bundle.Bytes())
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, fmt.Errorf("store session bundle: %w", err)
	}
	return v.Import(ctx, file.Name())
}

// dirtyWorktrees lists volume worktrees holding uncommitted or untracked work.
func dirtyWorktrees(ctx context.Context, m Metadata) ([]string, error) {
	var out bytes.Buffer
	if err := helper(ctx, m.Image, *m.Volume, dirtyScript, &out, m.Volume.Path); err != nil {
		return nil, fmt.Errorf("inspect volume %q: %w", m.Volume.Volume, err)
	}
	return strings.Fields(out.String()), nil
}

// checkVolume syncs branches and refuses when a worktree holds work a sync
// cannot carry. A missing volume passes.
func checkVolume(ctx context.Context, m Metadata) error {
	v := *m.Volume
	exists, err := docker.Client{}.VolumeExists(ctx, v.Volume)
	if err != nil || !exists {
		return err
	}
	if _, err := syncVolume(ctx, m); err != nil {
		return fmt.Errorf("%w; volume %q kept (hewitt rm --force discards it)", err, v.Volume)
	}
	dirty, err := dirtyWorktrees(ctx, m)
	if err != nil {
		return fmt.Errorf("%w; volume %q kept (hewitt rm --force discards it)", err, v.Volume)
	}
	if len(dirty) > 0 {
		return &DirtyVolumeError{Session: m.Name, Volume: v.Volume, Paths: dirty}
	}
	return nil
}

// dropVolume deletes the volume and the base anchor; imported branches stay.
func dropVolume(ctx context.Context, v workspace.Volume) error {
	if err := (docker.Client{}).RemoveVolume(ctx, v.Volume); err != nil {
		return err
	}
	return v.Release(ctx)
}

// nextName picks the first free "<agent>-<n>" session name. A name is taken
// by a saved session, a volume, or host branches in its namespace.
func nextName(ctx context.Context, store *Store, repo, agentName string) (string, error) {
	sessions, err := store.List()
	if err != nil {
		return "", err
	}
	used := map[string]bool{}
	for _, m := range sessions {
		used[m.Name] = true
	}
	for n := 1; n <= 100; n++ {
		name := agentName + "-" + strconv.Itoa(n)
		if used[name] {
			continue
		}
		if exists, err := (docker.Client{}).VolumeExists(ctx, "hewitt-"+name); err != nil {
			return "", err
		} else if exists {
			continue
		}
		if taken, err := workspace.NameTaken(ctx, repo, name); err != nil {
			return "", err
		} else if taken {
			continue
		}
		return name, nil
	}
	return "", errors.New("no free session name; pass --name")
}

// volumePreserved names what a failed start leaves for inspection.
func volumePreserved(v workspace.Volume) string {
	return fmt.Sprintf("volume %q preserved (hewitt sync imports its branches as %s/*)", v.Volume, v.Prefix())
}
