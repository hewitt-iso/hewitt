package session

import (
	"slices"
	"testing"
)

func TestTelemetryAllowDefaultsPortByScheme(t *testing.T) {
	for endpoint, want := range map[string]string{
		"https://otel.example.com":     "otel.example.com:443",
		"http://otel.example.com":      "otel.example.com:80",
		"https://collector.local:4318": "collector.local:4318",
	} {
		if got := telemetryAllow(endpoint); got != want {
			t.Errorf("%q: %q, want %q", endpoint, got, want)
		}
	}
}

func TestTelemetryEnvNamesEndpointForEveryAgent(t *testing.T) {
	env := telemetryEnv("https://otel.example.com")
	for _, want := range []string{"HEWITT_OTEL_ENDPOINT=https://otel.example.com", "OTEL_EXPORTER_OTLP_ENDPOINT=https://otel.example.com", "CLAUDE_CODE_ENABLE_TELEMETRY=1"} {
		if !slices.Contains(env, want) {
			t.Errorf("env %q lacks %q", env, want)
		}
	}
}
