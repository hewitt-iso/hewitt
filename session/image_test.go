package session

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunImagePrecedence(t *testing.T) {
	for _, tt := range []struct {
		name, client, file, image, mode, target, want string
	}{
		{name: "workspace default", client: "", want: "ghcr.io/hewitt-iso/hewitt:latest"},
		{name: "codex default", client: "codex", want: "ghcr.io/hewitt-iso/hewitt:latest"},
		{name: "claude default", client: "claude", want: "ghcr.io/hewitt-iso/hewitt:latest"},
		{name: "opencode default", client: "opencode", want: "ghcr.io/hewitt-iso/hewitt:latest"},
		{name: "file overrides default", client: "codex", file: "image: project:test\n", want: "project:test"},
		{name: "flag overrides file", client: "codex", file: "image: project:test\n", image: "custom:test", want: "custom:test"},
		{name: "exec has no default image", client: "codex", mode: "exec", target: "external"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repo := t.TempDir()
			if tt.file != "" {
				if err := os.WriteFile(filepath.Join(repo, "hewitt.yml"), []byte(tt.file), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			s, err := mergeSettings(AgentOptions{Agent: tt.client, Repository: repo, Image: tt.image, Mode: tt.mode, Target: tt.target})
			if err != nil {
				t.Fatal(err)
			}
			if s.Image != tt.want {
				t.Fatalf("image = %q, want %q", s.Image, tt.want)
			}
		})
	}
}
