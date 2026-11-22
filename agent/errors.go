package agent

import (
	"fmt"
	"strings"
)

// UnknownAgentError names an agent that no manifest describes.
type UnknownAgentError struct {
	Name      string
	Supported []string
}

func (e *UnknownAgentError) Error() string {
	return fmt.Sprintf("unknown agent %q (supported: %s)", e.Name, strings.Join(e.Supported, ", "))
}

func (*UnknownAgentError) Hint() string {
	return "add a manifest as <data_dir>/agents/<name>.yaml to run another agent; docs/agents.md describes the format"
}

// ManifestError is an agent manifest that cannot be used.
type ManifestError struct {
	Source string
	Err    error
}

func (e *ManifestError) Error() string { return fmt.Sprintf("agent manifest %s: %v", e.Source, e.Err) }
func (*ManifestError) Hint() string    { return "docs/agents.md describes the manifest format" }
func (e *ManifestError) Unwrap() error { return e.Err }
