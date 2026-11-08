package session

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
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
// reaches the outside through Docker's default network and permits only
// tunnels to the allowed hosts. Nothing is started; on failure, created
// resources are reported.
func createEgress(ctx context.Context, dataDir, image string, hosts []string) (Egress, error) {
	allow, conf, err := egressConfig(hosts)
	if err != nil {
		return Egress{}, err
	}
	token := strings.ToLower(rand.Text())
	e := Egress{Network: "hewitt-egress-" + token, ConfigDir: filepath.Join(dataDir, "egress", token), Hosts: hosts}
	if err := os.MkdirAll(e.ConfigDir, 0o700); err != nil {
		return e, fmt.Errorf("create egress configuration: %w", err)
	}
	for name, content := range map[string]string{"allow": allow, "tinyproxy.conf": conf} {
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

// egressConfig renders the tinyproxy filter and configuration for hosts, each
// "host" (port 443) or "host:port". The filter matches the whole request
// target, so only CONNECT to a listed host and port passes; plain HTTP
// requests carry a full URL, match no rule, and are refused.
func egressConfig(hosts []string) (allow, conf string, err error) {
	var filter strings.Builder
	var ports []string
	for _, entry := range hosts {
		host, port, hasPort := strings.Cut(entry, ":")
		if !hasPort {
			port = "443"
		}
		if !egressHost.MatchString(host) {
			return "", "", fmt.Errorf("egress.allow host %q must be a lowercase DNS name, optionally prefixed with *.", entry)
		}
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
			return "", "", fmt.Errorf("egress.allow entry %q must use a port from 1 to 65535", entry)
		}
		if !slices.Contains(ports, port) {
			ports = append(ports, port)
		}
		name, wildcard := strings.CutPrefix(host, "*.")
		quoted := strings.ReplaceAll(name, ".", `\.`)
		fmt.Fprintf(&filter, "^%s:%s$\n", quoted, port)
		if wildcard {
			fmt.Fprintf(&filter, `\.%s:%s$`+"\n", quoted, port)
		}
	}
	conf = "Port " + egressPort + "\nListen 0.0.0.0\nTimeout 600\nMaxClients 100\nLogLevel Notice\nDisableViaHeader Yes\n"
	for _, port := range ports {
		conf += "ConnectPort " + port + "\n"
	}
	conf += "FilterURLs On\nFilterExtended On\nFilterDefaultDeny Yes\nFilter \"/hewitt/egress/allow\"\n"
	return filter.String(), conf, nil
}

func (e Egress) preserved() string {
	return fmt.Sprintf("egress proxy %q and network %q preserved", e.Proxy, e.Network)
}
