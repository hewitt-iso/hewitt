package session

import (
	"strings"
	"testing"
)

func TestEgressConfigAllowsOnlyListedHostPorts(t *testing.T) {
	allow, conf, err := egressConfig([]string{"api.anthropic.com", "*.githubcopilot.com", "collector.local:4318"})
	if err != nil {
		t.Fatal(err)
	}
	want := `^api\.anthropic\.com:443$` + "\n" +
		`^githubcopilot\.com:443$` + "\n" + `\.githubcopilot\.com:443$` + "\n" +
		`^collector\.local:4318$` + "\n"
	if allow != want {
		t.Fatalf("allow = %q, want %q", allow, want)
	}
	// FilterURLs matches the whole target, so plain HTTP URLs match no rule.
	for _, line := range []string{"ConnectPort 443\n", "ConnectPort 4318\n", "FilterURLs On\n", "FilterExtended On\n", "FilterDefaultDeny Yes\n"} {
		if strings.Count(conf, line) != 1 {
			t.Errorf("conf missing %q once: %s", line, conf)
		}
	}
}

func TestEgressConfigRejectsInvalidEntries(t *testing.T) {
	for _, entry := range []string{"Example.com", "http://example.com", "example.com:", "example.com:0", "example.com:65536", "example.com:0443", "example.com:443:1"} {
		if _, _, err := egressConfig([]string{entry}); err == nil || !strings.Contains(err.Error(), "egress.allow") {
			t.Errorf("%q: err = %v", entry, err)
		}
	}
}
