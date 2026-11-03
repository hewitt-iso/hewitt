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
		{name: "codex default", client: "codex", want: "ghcr.io/hewitt-iso/hewitt-codex:latest"},
		{name: "claude default", client: "claude", want: "ghcr.io/hewitt-iso/hewitt-claude:latest"},
		{name: "file overrides default", client: "codex", file: "FROM project:test\n", want: "project:test"},
		{name: "flag overrides file", client: "codex", file: "FROM project:test\n", image: "custom:test", want: "custom:test"},
		{name: "exec has no default image", client: "codex", mode: "exec", target: "external"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repo := t.TempDir()
			if tt.file != "" {
				if err := os.WriteFile(filepath.Join(repo, "Hewittfile"), []byte(tt.file), 0o600); err != nil {
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
