package session

import (
	"net/url"
)

// telemetryEnv exports OpenTelemetry settings to every agent in the
// container. Claude Code and OpenCode read the standard OTLP variables;
// hewitt-agent turns HEWITT_OTEL_ENDPOINT into Codex's -c otel overrides.
// Agents without OpenTelemetry support ignore them.
func telemetryEnv(endpoint string) []string {
	return []string{
		"HEWITT_OTEL_ENDPOINT=" + endpoint,
		"CLAUDE_CODE_ENABLE_TELEMETRY=1",
		"OTEL_METRICS_EXPORTER=otlp",
		"OTEL_LOGS_EXPORTER=otlp",
		"OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf",
		"OTEL_EXPORTER_OTLP_ENDPOINT=" + endpoint,
	}
}

// telemetryAllow is the egress.allow entry for a validated endpoint.
func telemetryAllow(endpoint string) string {
	u, _ := url.Parse(endpoint)
	port := u.Port()
	if port == "" {
		port = "443"
		if u.Scheme == "http" {
			port = "80"
		}
	}
	return u.Hostname() + ":" + port
}
