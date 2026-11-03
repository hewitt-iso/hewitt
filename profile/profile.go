// Package profile provisions private directories for native agent state.
package profile

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
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

// PrepareTools creates and validates the shared tools directory (<dataDir>/tools)
// where on-demand agent installations are persisted across workspace containers.
func PrepareTools(dataDir string) (string, error) {
	if !filepath.IsAbs(dataDir) {
		return "", fmt.Errorf("profile data directory must be absolute")
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return "", fmt.Errorf("create data directory: %w", err)
	}
	root, err := filepath.EvalSymlinks(dataDir)
	if err != nil {
		return "", fmt.Errorf("resolve data directory: %w", err)
	}
	tools := filepath.Join(root, "tools")
	if err := privateDir(tools); err != nil {
		return "", err
	}
	if err := privateDir(filepath.Join(tools, "bin")); err != nil {
		return "", err
	}
	return tools, nil
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

const tokenFile = "token.env"

// SaveToken stores a long-lived token that the user copied from the native
// client's official login output, as a private Docker env file in the profile.
// The value is validated but never logged; the write is atomic.
func SaveToken(dir, variable, token string) error {
	if token == "" || strings.ContainsFunc(token, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return fmt.Errorf("token must be nonempty and contain no whitespace or control characters")
	}
	tmp, err := os.CreateTemp(dir, ".token-*")
	if err != nil {
		return fmt.Errorf("create token file: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(variable + "=" + token + "\n"); err != nil {
		tmp.Close()
		return fmt.Errorf("write token file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write token file: %w", err)
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, tokenFile)); err != nil {
		return fmt.Errorf("save token file: %w", err)
	}
	return nil
}

// TokenFile returns the profile's token env file, or "" when none was saved.
// Symlinks and files readable by group or other users are refused.
func TokenFile(dir string) (string, error) {
	path := filepath.Join(dir, tokenFile)
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("inspect token file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("token file %q must be a private regular file", path)
	}
	return path, nil
}

// RemoveToken deletes a saved token; a missing token is not an error.
func RemoveToken(dir string) error {
	if err := os.Remove(filepath.Join(dir, tokenFile)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove token file: %w", err)
	}
	return nil
}
