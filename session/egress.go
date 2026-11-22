package session

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hewitt-iso/hewitt/agent"
	"github.com/hewitt-iso/hewitt/docker"
	"github.com/hewitt-iso/hewitt/gateway"
	"github.com/hewitt-iso/hewitt/profile"
)

// Egress records the Hewitt-owned gateway container and network of one
// session. Like session containers, they are preserved for explicit cleanup.
// The JSON names predate the gateway, which replaced the egress proxy.
type Egress struct {
	Network   string   `json:"network"`
	Proxy     string   `json:"proxy"`
	ConfigDir string   `json:"config_dir"`
	Hosts     []string `json:"hosts"`
	// Open gateways pass every host; others hold unlisted hosts for approval.
	Open bool `json:"open,omitempty"`
	// Observer is the container that holds agents' access to other agents'
	// logins and to credential files, when the session has one.
	Observer string `json:"observer,omitempty"`
}

// helpers lists the session's Hewitt-owned helper containers.
func (e Egress) helpers() []string {
	if e.Observer != "" {
		return []string{e.Proxy, e.Observer}
	}
	return []string{e.Proxy}
}

// StateDir holds the session's requests for approval and their decisions.
func (e Egress) StateDir() string { return filepath.Join(e.ConfigDir, "state") }

const (
	egressAlias = "hewitt-egress"
	egressPort  = "3128"
	// DefaultEgressImage is the gateway image, published with the workspace
	// image; make image-egress builds it locally under the same tag.
	DefaultEgressImage = "ghcr.io/hewitt-iso/egress:latest"
	// gatewayCA is where agent containers find the certificate that signs the
	// gateway's certificates for injected hosts.
	gatewayCA = "/hewitt/gateway-ca.crt"
)

// proxyEnv points clients at the gateway; both spellings are common. Claude
// Code trusts the gateway's CA through NODE_EXTRA_CA_CERTS. An open gateway
// serves only HTTPS: its network reaches the outside, so plain HTTP, which
// the gateway refuses, goes direct.
func proxyEnv(open bool) []string {
	proxy := "http://" + egressAlias + ":" + egressPort
	env := []string{"HTTPS_PROXY=" + proxy, "https_proxy=" + proxy,
		"NO_PROXY=localhost,127.0.0.1", "no_proxy=localhost,127.0.0.1", "NODE_EXTRA_CA_CERTS=" + gatewayCA}
	if !open {
		env = append(env, "HTTP_PROXY="+proxy, "http_proxy="+proxy)
	}
	return env
}

// runEnv identifies one agent connection to Hewitt's gateway and observer:
// HEWITT_RUN and HEWITT_AGENT mark its processes, and the proxy credentials
// mark its connections, so requests that need approval name the agent and
// abort can stop it. Requests without these are attributed to no connection.
func runEnv(e *Egress, run, agent string) []string {
	env := []string{"HEWITT_RUN=" + run, "HEWITT_AGENT=" + agent}
	if e == nil {
		return env
	}
	proxy := "http://" + run + ":" + agent + "@" + egressAlias + ":" + egressPort
	env = append(env, "HTTPS_PROXY="+proxy, "https_proxy="+proxy)
	if !e.Open {
		env = append(env, "HTTP_PROXY="+proxy, "http_proxy="+proxy)
	}
	return env
}

// caDir is the user's gateway CA, shared by all sessions. Only gateway
// containers mount it; agents mount only its certificate.
func caDir(dataDir string) string { return filepath.Join(dataDir, "gateway-ca") }

// createGateway creates a network and a stopped gateway container that
// reaches the outside through Docker's default network. A restricted gateway
// (open false) has an internal network and passes only tunnels to hosts;
// other hosts wait for approval. Every gateway replaces the Claude
// placeholder with the token saved for profileID. Nothing is started; on
// failure, created resources are reported.
func createGateway(ctx context.Context, dataDir, image, profileID string, open bool, hosts []string, agents []agent.Manifest) (Egress, error) {
	if !open {
		if _, err := gateway.ParseRules(hosts); err != nil {
			return Egress{}, err
		}
	}
	if err := gateway.EnsureCA(caDir(dataDir)); err != nil {
		return Egress{}, err
	}
	token := strings.ToLower(rand.Text())
	e := Egress{Network: "hewitt-egress-" + token, ConfigDir: filepath.Join(dataDir, "egress", token), Hosts: hosts, Open: open}
	if err := os.MkdirAll(e.StateDir(), 0o700); err != nil {
		return e, fmt.Errorf("create gateway configuration: %w", err)
	}
	cfg := gateway.Config{Listen: "0.0.0.0:" + egressPort, Open: open, CADir: "/hewitt/ca", StateDir: "/hewitt/state"}
	mounts := []docker.BindMount{
		{Source: filepath.Join(e.ConfigDir, "config.json"), Target: "/hewitt/gateway/config.json", ReadOnly: true},
		{Source: e.StateDir(), Target: "/hewitt/state"},
		{Source: caDir(dataDir), Target: "/hewitt/ca", ReadOnly: true},
	}
	// Only the gateway mounts the saved tokens it puts into requests.
	for _, a := range agents {
		if a.Token == nil || len(a.Token.Hosts) == 0 {
			continue
		}
		secrets, err := profile.PrepareSecrets(dataDir, a.Name, profileID)
		if err != nil {
			return e, err
		}
		dir := "/hewitt/secrets/" + a.Name
		mounts = append(mounts, docker.BindMount{Source: secrets, Target: dir, ReadOnly: true})
		cfg.Inject = append(cfg.Inject, gateway.Injection{Hosts: a.Token.Hosts, Placeholder: a.Token.Placeholder,
			TokenFile: dir + "/token.env", Variable: a.Token.Variable})
	}
	if !open {
		cfg.Allow = hosts
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return e, fmt.Errorf("encode gateway configuration: %w", err)
	}
	if err := os.WriteFile(filepath.Join(e.ConfigDir, "config.json"), data, 0o600); err != nil {
		return e, fmt.Errorf("write gateway configuration: %w", err)
	}
	client := docker.Client{}
	if _, err := client.CreateNetwork(ctx, e.Network, !open); err != nil {
		return e, err
	}
	proxy, err := client.Create(ctx, docker.CreateOptions{
		Image: image, Entrypoint: "hewitt", Command: []string{"gateway", "/hewitt/gateway/config.json"},
		Mounts: mounts,
		User:   fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), Workdir: "/tmp",
		ReadOnlyRoot: true, DisableTTY: true,
	})
	if err != nil {
		return e, fmt.Errorf("gateway network %q preserved: %w", e.Network, err)
	}
	e.Proxy = proxy
	if err := client.ConnectNetwork(ctx, e.Network, proxy, egressAlias); err != nil {
		return e, err
	}
	return e, nil
}

// gatewayMount gives an agent container the gateway CA certificate.
func gatewayMount(dataDir string) docker.BindMount {
	return docker.BindMount{Source: gateway.CACertPath(caDir(dataDir)), Target: gatewayCA, ReadOnly: true}
}

// openPorts lists the entries that name a port other than 443.
func openPorts(hosts []string) []string {
	var open []string
	for _, entry := range hosts {
		if _, port, ok := strings.Cut(entry, ":"); ok && port != "443" {
			open = append(open, entry)
		}
	}
	return open
}

func (e Egress) preserved() string {
	return fmt.Sprintf("gateway %q and network %q preserved", e.Proxy, e.Network)
}
