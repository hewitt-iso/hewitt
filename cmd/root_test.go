package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRootCommand(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	broken := filepath.Join(home, "broken.json")
	if err := os.WriteFile(broken, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(home, "missing.json")

	for _, tt := range []struct {
		name    string
		args    []string
		want    string
		wantErr string
	}{
		{name: "no arguments", want: "Usage:"},
		{name: "help", args: []string{"--help"}, want: "Session execution is not\nimplemented yet."},
		{name: "short help", args: []string{"-h"}, want: "--config"},
		{name: "version", args: []string{"--version"}, want: "hewitt v0.1.0-test\n"},
		{name: "unknown command", args: []string{"unknown"}, wantErr: "unknown command"},
		{name: "unknown flag", args: []string{"--unknown"}, wantErr: "unknown flag"},
		{name: "missing flag value", args: []string{"--config"}, wantErr: "flag needs an argument"},
		{name: "missing config", args: []string{"--config", missing}, wantErr: "read config"},
		{name: "invalid config", args: []string{"--config", broken}, wantErr: "parse config"},
		{name: "help bypasses config", args: []string{"--config", broken, "--help"}, want: "Usage:"},
		{name: "version bypasses config", args: []string{"--config", missing, "--version"}, want: "hewitt v0.1.0-test\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			root := NewRootCommand("v0.1.0-test")
			root.SetOut(&stdout)
			root.SetErr(&stderr)
			root.SetArgs(tt.args)
			err := root.Execute()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want containing %q", err, tt.wantErr)
				}
				if stdout.Len() != 0 {
					t.Errorf("unexpected stdout on error: %q", stdout.String())
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(stdout.String(), tt.want) {
					t.Errorf("stdout = %q, want containing %q", stdout.String(), tt.want)
				}
			}
			if stderr.Len() != 0 {
				t.Errorf("command printed diagnostics instead of returning them: %q", stderr.String())
			}
		})
	}
}
