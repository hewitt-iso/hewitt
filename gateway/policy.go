package gateway

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Config is written by Hewitt on the host and read by the gateway.
type Config struct {
	// Listen is the proxy address, such as 0.0.0.0:3128.
	Listen string `json:"listen"`
	// Open passes every host; otherwise only Allow entries pass and other
	// hosts wait for approval in StateDir.
	Open bool `json:"open"`
	// Allow entries are "host" (port 443) or "host:port"; a host may start
	// with "*." to match its subdomains.
	Allow []string `json:"allow,omitempty"`
	// Inject replaces placeholder credentials for selected hosts.
	Inject []Injection `json:"inject,omitempty"`
	// CADir holds the CA created by EnsureCA; StateDir holds requests and
	// decisions shared with the host.
	CADir    string `json:"ca_dir"`
	StateDir string `json:"state_dir"`
	// Wait is how long a request to an unlisted host waits for a decision, in
	// seconds; a denied or unanswered request is refused.
	Wait int `json:"wait"`
}

// Injection replaces Placeholder in request headers to Hosts with the value
// of Variable in TokenFile, a KEY=VALUE file read for each connection so a new
// login applies without restarting the gateway.
type Injection struct {
	Hosts       []string `json:"hosts"`
	Placeholder string   `json:"placeholder"`
	TokenFile   string   `json:"token_file"`
	Variable    string   `json:"variable"`
}

// LoadConfig reads and validates a configuration file.
func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read gateway configuration: %w", err)
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return Config{}, fmt.Errorf("parse gateway configuration: %w", err)
	}
	if _, err := ParseRules(c.Allow); err != nil {
		return Config{}, err
	}
	for _, in := range c.Inject {
		if in.Placeholder == "" || in.TokenFile == "" || in.Variable == "" || len(in.Hosts) == 0 {
			return Config{}, fmt.Errorf("gateway injection needs hosts, placeholder, token_file, and variable")
		}
	}
	return c, nil
}

var hostPattern = regexp.MustCompile(`^(\*\.)?[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

// Rule allows one host, or every subdomain of it, on one port.
type Rule struct {
	Host     string
	Wildcard bool
	Port     string
}

// ParseRules validates allowlist entries.
func ParseRules(entries []string) ([]Rule, error) {
	var rules []Rule
	for _, entry := range entries {
		host, port, hasPort := strings.Cut(entry, ":")
		if !hasPort {
			port = "443"
		}
		if !hostPattern.MatchString(host) {
			return nil, fmt.Errorf("egress.allow host %q must be a lowercase DNS name, optionally prefixed with *.", entry)
		}
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
			return nil, fmt.Errorf("egress.allow entry %q must use a port from 1 to 65535", entry)
		}
		name, wildcard := strings.CutPrefix(host, "*.")
		rules = append(rules, Rule{Host: name, Wildcard: wildcard, Port: port})
	}
	return rules, nil
}

// match reports whether a rule covers host:port. A wildcard matches the
// host itself and its subdomains, as the earlier tinyproxy filter did.
func match(rules []Rule, host, port string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, r := range rules {
		if r.Port != port {
			continue
		}
		if host == r.Host || r.Wildcard && strings.HasSuffix(host, "."+r.Host) {
			return true
		}
	}
	return false
}

// splitTarget parses a CONNECT target; a missing port means 443.
func splitTarget(target string) (host, port string, err error) {
	host, port, err = net.SplitHostPort(target)
	if err != nil {
		return "", "", fmt.Errorf("invalid CONNECT target %q", target)
	}
	if host == "" || strings.ContainsAny(host, "/\\ ") {
		return "", "", fmt.Errorf("invalid CONNECT target %q", target)
	}
	return strings.ToLower(host), port, nil
}
