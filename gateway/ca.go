// Package gateway is the per-session network gateway: an HTTP CONNECT proxy
// that applies a host policy, holds requests to unlisted hosts for the user's
// approval, and replaces placeholder credentials with real ones for selected
// provider hosts, so agents never hold those credentials. It runs in its own
// container; agents cannot read its configuration, credentials, or CA key.
package gateway

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	caCertFile = "ca.crt"
	caKeyFile  = "ca.key"
)

// CA signs the certificates the gateway presents for hosts whose requests it
// rewrites. Agent containers trust only its certificate, never its key.
type CA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey

	mu     sync.Mutex
	leaves map[string]*tls.Certificate
}

// CACertPath is the certificate that agent containers trust.
func CACertPath(dir string) string { return filepath.Join(dir, caCertFile) }

// EnsureCA creates a CA in dir unless one exists. The key file is private to
// the user; dir is mounted only into gateway containers.
func EnsureCA(dir string) error {
	if _, err := os.Stat(filepath.Join(dir, caKeyFile)); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect gateway CA: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create gateway CA directory: %w", err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate gateway CA key: %w", err)
	}
	template := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: "Hewitt gateway CA", Organization: []string{"Hewitt"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return fmt.Errorf("create gateway CA: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return fmt.Errorf("encode gateway CA key: %w", err)
	}
	// The certificate is written last: its presence marks a complete CA.
	if err := writePEM(filepath.Join(dir, caKeyFile), "EC PRIVATE KEY", keyDER, 0o600); err != nil {
		return err
	}
	return writePEM(filepath.Join(dir, caCertFile), "CERTIFICATE", der, 0o644)
}

// LoadCA reads a CA created by EnsureCA.
func LoadCA(dir string) (*CA, error) {
	certPEM, err := os.ReadFile(filepath.Join(dir, caCertFile))
	if err != nil {
		return nil, fmt.Errorf("read gateway CA: %w", err)
	}
	keyPEM, err := os.ReadFile(filepath.Join(dir, caKeyFile))
	if err != nil {
		return nil, fmt.Errorf("read gateway CA key: %w", err)
	}
	certBlock, _ := pem.Decode(certPEM)
	keyBlock, _ := pem.Decode(keyPEM)
	if certBlock == nil || keyBlock == nil {
		return nil, fmt.Errorf("gateway CA in %s is not PEM", dir)
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse gateway CA: %w", err)
	}
	key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse gateway CA key: %w", err)
	}
	return &CA{cert: cert, key: key, leaves: map[string]*tls.Certificate{}}, nil
}

// leaf returns a certificate for host signed by the CA.
func (ca *CA) leaf(host string) (*tls.Certificate, error) {
	ca.mu.Lock()
	defer ca.mu.Unlock()
	if cert, ok := ca.leaves[host]; ok && time.Until(cert.Leaf.NotAfter) > time.Hour {
		return cert, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate certificate key: %w", err)
	}
	template := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(7 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		return nil, fmt.Errorf("sign certificate for %s: %w", host, err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	cert := &tls.Certificate{Certificate: [][]byte{der, ca.cert.Raw}, PrivateKey: key, Leaf: parsed}
	ca.leaves[host] = cert
	return cert, nil
}

func serial() *big.Int {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		panic(err)
	}
	return n
}

func writePEM(path, kind string, der []byte, mode os.FileMode) error {
	data := pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der})
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	return nil
}
