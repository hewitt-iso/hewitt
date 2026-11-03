package session

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/hewitt-iso/hewitt/docker"
)

// Egress records the Hewitt-owned proxy and internal network of one restricted
// session. Like session containers, they are preserved for explicit cleanup.
type Egress struct {
	Network   string   `json:"network"`
	Proxy     string   `json:"proxy"`
	ConfigDir string   `json:"config_dir"`
	Hosts     []string `json:"hosts"`
}

const (
	egressAlias = "hewitt-egress"
	egressPort  = "3128"
	// DefaultEgressImage is built by make image-egress.
	DefaultEgressImage = "hewitt/egress:local"
)

var egressHost = regexp.MustCompile(`^(\*\.)?[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

// proxyEnv points clients at the egress proxy; both spellings are common.
func proxyEnv() []string {
	proxy := "http://" + egressAlias + ":" + egressPort
	return []string{"HTTPS_PROXY=" + proxy, "HTTP_PROXY=" + proxy, "https_proxy=" + proxy, "http_proxy=" + proxy,
		"NO_PROXY=localhost,127.0.0.1", "no_proxy=localhost,127.0.0.1"}
}

// createEgress creates an internal network and a stopped proxy container that
// reaches the outside through Docker's default network and permits HTTPS only
// to hosts. Nothing is started; on failure, created resources are reported.
func createEgress(ctx context.Context, dataDir, image string, hosts []string) (Egress, error) {
	for _, host := range hosts {
		if !egressHost.MatchString(host) {
			return Egress{}, fmt.Errorf("egress.allow host %q must be a lowercase DNS name, optionally prefixed with *.", host)
		}
	}
	token := strings.ToLower(rand.Text())
	e := Egress{Network: "hewitt-egress-" + token, ConfigDir: filepath.Join(dataDir, "egress", token), Hosts: hosts}
	if err := os.MkdirAll(e.ConfigDir, 0o700); err != nil {
		return e, fmt.Errorf("create egress configuration: %w", err)
	}
	var allow strings.Builder
	for _, host := range hosts {
		// tinyproxy matches the requested host with POSIX basic expressions.
		name, wildcard := strings.CutPrefix(host, "*.")
		quoted := strings.ReplaceAll(name, ".", `\.`)
		fmt.Fprintf(&allow, "^%s$\n", quoted)
		if wildcard {
			fmt.Fprintf(&allow, `\.%s$`+"\n", quoted)
		}
	}
	conf := "Port " + egressPort + "\nListen 0.0.0.0\nTimeout 600\nMaxClients 100\nLogLevel Notice\n" +
		"DisableViaHeader Yes\nConnectPort 443\nFilterDefaultDeny Yes\nFilter \"/hewitt/egress/allow\"\n"
	for name, content := range map[string]string{"allow": allow.String(), "tinyproxy.conf": conf} {
		if err := os.WriteFile(filepath.Join(e.ConfigDir, name), []byte(content), 0o600); err != nil {
			return e, fmt.Errorf("write egress configuration: %w", err)
		}
	}
	client := docker.Client{}
	if _, err := client.CreateNetwork(ctx, e.Network, true); err != nil {
		return e, err
	}
	proxy, err := client.Create(ctx, docker.CreateOptions{
		Image: image, Entrypoint: "tinyproxy", Command: []string{"-d", "-c", "/hewitt/egress/tinyproxy.conf"},
		Mounts: []docker.BindMount{{Source: e.ConfigDir, Target: "/hewitt/egress", ReadOnly: true}},
		User:   fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), Workdir: "/tmp",
		ReadOnlyRoot: true, DisableTTY: true,
	})
	if err != nil {
		return e, fmt.Errorf("egress network %q preserved: %w", e.Network, err)
	}
	e.Proxy = proxy
	if err := client.ConnectNetwork(ctx, e.Network, proxy, egressAlias); err != nil {
		return e, err
	}
	return e, nil
}

func (e Egress) preserved() string {
	return fmt.Sprintf("egress proxy %q and network %q preserved", e.Proxy, e.Network)
}
