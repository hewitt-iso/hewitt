package config

import (
	"strings"
	"testing"
)

func TestTelemetryEndpointIsNormalized(t *testing.T) {
	for raw, want := range map[string]string{
		"https://otel.example.com":     "https://otel.example.com",
		"http://172.17.0.1:4318/":      "http://172.17.0.1:4318",
		"https://collector.local:4318": "https://collector.local:4318",
	} {
		p, err := ParseProject([]byte("telemetry:\n  endpoint: "+raw+"\n"), "hewitt.yml")
		if err != nil || p.Telemetry != want {
			t.Errorf("%q: telemetry = %q, %v; want %q", raw, p.Telemetry, err, want)
		}
	}
}

func TestTelemetryEndpointRejectsUnsafeValues(t *testing.T) {
	for _, raw := range []string{
		"otel.example.com:4318",                // no scheme
		"ftp://otel.example.com",               // wrong scheme
		"https://OTEL.example.com",             // uppercase host
		"https://user:secret@otel.example.com", // credentials
		"https://otel.example.com/v1/traces",   // path
		"https://otel.example.com?token=x",     // query
		"https://otel.example.com:0",           // port out of range
		"https://otel.example.com:",            // empty port
		`https://otel".example.com`,            // quote breaking Codex's TOML
	} {
		if _, err := ParseProject([]byte("telemetry:\n  endpoint: '"+raw+"'\n"), "hewitt.yml"); err == nil || !strings.Contains(err.Error(), "telemetry.endpoint") {
			t.Errorf("%q: err = %v", raw, err)
		}
	}
}
