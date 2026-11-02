package workspace

import (
	"crypto/rand"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Copy identifies a private snapshot of selected source paths, including their
// uncommitted changes. The source is never modified; changes made in the copy
// are returned manually from Path.
type Copy struct {
	Source string   `json:"source"`
	Path   string   `json:"path"`
	Paths  []string `json:"paths"`
}

// CreateCopy copies paths, relative to source, into <dataDir>/copies/<id> at the
// same relative locations. Symlinks are recreated, never followed; sockets and
// named pipes are skipped. A failure leaves the partial copy for inspection.
func CreateCopy(dataDir, source string, paths []string) (Copy, error) {
	if !filepath.IsAbs(dataDir) || !filepath.IsAbs(source) {
		return Copy{}, fmt.Errorf("copy directories must be absolute")
	}
	if len(paths) == 0 {
		paths = []string{"."}
	}
	for _, path := range paths {
		if !filepath.IsLocal(path) && filepath.Clean(path) != "." {
			return Copy{}, fmt.Errorf("copy path %q must stay inside %q", path, source)
		}
	}
	root := filepath.Join(dataDir, "copies")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return Copy{}, fmt.Errorf("create copy directory: %w", err)
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return Copy{}, fmt.Errorf("resolve copy directory: %w", err)
	}
	c := Copy{Source: source, Path: filepath.Join(root, strings.ToLower(rand.Text())), Paths: paths}
	if err := c.Validate(); err != nil {
		return c, err
	}
	if err := os.Mkdir(c.Path, 0o700); err != nil {
		return c, fmt.Errorf("create copy %q: %w", c.Path, err)
	}
	for _, path := range paths {
		if err := copyTree(filepath.Join(source, path), filepath.Join(c.Path, path)); err != nil {
			return c, fmt.Errorf("copy %q into %q (partial copy preserved): %w", path, c.Path, err)
		}
	}
	return c, nil
}

// Validate checks persisted paths before they can become container mounts.
func (c Copy) Validate() error {
	for _, path := range []string{c.Source, c.Path} {
		if !filepath.IsAbs(path) || strings.ContainsAny(path, ",\x00\r\n") {
			return fmt.Errorf("copy paths must be absolute and contain no commas or control characters")
		}
	}
	return nil
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		switch mode := info.Mode(); {
		case mode.IsDir():
			return os.MkdirAll(target, mode.Perm()|0o700)
		case mode&fs.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return err
			}
			return os.Symlink(link, target)
		case mode.IsRegular():
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return err
			}
			return copyFile(path, target, mode.Perm())
		case mode&(fs.ModeSocket|fs.ModeNamedPipe) != 0:
			return nil
		default:
			return fmt.Errorf("unsupported file type at %q", path)
		}
	})
}

func copyFile(src, dst string, perm fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
