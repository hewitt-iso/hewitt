// Package profile provisions private directories for native agent state.
package profile

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

var validID = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// ValidateID accepts a single portable path component, never a filesystem path.
func ValidateID(id string) error {
	if !validID.MatchString(id) {
		return fmt.Errorf("profile must be 1-64 lowercase letters, digits, underscores or hyphens, starting with a letter or digit")
	}
	return nil
}

// Prepare preserves native state and rejects symlinks and permissive existing
// profile directories. It never reads or rewrites the client's credential files.
// The data root is trusted user configuration; its parents may be OS symlinks.
func Prepare(dataDir, client, id string) (string, error) {
	if !filepath.IsAbs(dataDir) {
		return "", fmt.Errorf("profile data directory must be absolute")
	}
	if err := ValidateID(client); err != nil {
		return "", fmt.Errorf("client: %w", err)
	}
	if err := ValidateID(id); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return "", fmt.Errorf("create data directory: %w", err)
	}
	root, err := filepath.EvalSymlinks(dataDir)
	if err != nil {
		return "", fmt.Errorf("resolve data directory: %w", err)
	}
	current := root
	for _, component := range []string{"profiles", client, id} {
		current = filepath.Join(current, component)
		if err := privateDir(current); err != nil {
			return "", err
		}
	}
	for _, child := range []string{"home", "config", "data", "state", "cache", client} {
		if err := privateDir(filepath.Join(current, child)); err != nil {
			return "", err
		}
	}
	return current, nil
}

func privateDir(path string) error {
	if err := os.Mkdir(path, 0o700); err != nil && !os.IsExist(err) {
		return fmt.Errorf("create profile directory: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect profile directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("profile directory %q must be a real directory, not a symlink", path)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("profile directory %q must not be accessible to group or other users", path)
	}
	return nil
}
