package session

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hewitt-iso/hewitt/config"
)

// A hewitt.yml arrives with the repository, so its sensitive settings apply
// only after the user trusted that exact file: the record holds the SHA-256
// of the contents the user reviewed, and any change requires trust again.

func trustRecord(dataDir, path string) (string, error) {
	if !filepath.IsAbs(dataDir) {
		return "", fmt.Errorf("session data directory must be an absolute path")
	}
	sum := sha256.Sum256([]byte(path))
	return filepath.Join(dataDir, "trust", hex.EncodeToString(sum[:])), nil
}

// TrustProject records that the file at path with the given content digest
// may apply its sensitive settings.
func TrustProject(dataDir, path, digest string) error {
	record, err := trustRecord(dataDir, path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(record), 0o700); err != nil {
		return fmt.Errorf("create trust directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(record), ".trust-*")
	if err != nil {
		return fmt.Errorf("record trust: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(digest + " " + path + "\n"); err != nil {
		tmp.Close()
		return fmt.Errorf("record trust: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("record trust: %w", err)
	}
	if err := os.Rename(tmp.Name(), record); err != nil {
		return fmt.Errorf("record trust: %w", err)
	}
	return nil
}

// checkTrust refuses a project file whose sensitive settings were not trusted
// in their current form. Files without sensitive settings need no trust.
func checkTrust(dataDir string, p config.Project) error {
	keys := p.Sensitive()
	if p.Path == "" || len(keys) == 0 {
		return nil
	}
	record, err := trustRecord(dataDir, p.Path)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(record)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read trust record: %w", err)
	}
	digest, _, _ := strings.Cut(string(data), " ")
	if digest == p.Digest {
		return nil
	}
	return &UntrustedProjectError{Path: p.Path, Digest: p.Digest, Keys: keys, Changed: len(data) > 0}
}
