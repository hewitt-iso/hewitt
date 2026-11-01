// Package cmd defines Hewitt's command-line interface.
package cmd

import (
	"github.com/hewitt-iso/hewitt/config"
	"github.com/spf13/cobra"
)

// NewRootCommand returns an independent command tree for one invocation.
func NewRootCommand(version string) *cobra.Command {
	var configPath string
	cmd := &cobra.Command{
		Use:           "hewitt",
		Short:         "Run coding agents in isolated local workspaces",
		Long:          "Hewitt is a local-first CLI for coding agents in Docker containers and Git worktrees.\n\nThis initial version provides CLI and configuration foundations. Session commands\nare not implemented yet.",
		Version:       version,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Validate configuration on normal invocation. Help and version bypass
			// loading so they remain available even when configuration is broken.
			if _, err := config.Load(configPath); err != nil {
				return err
			}
			return cmd.Help()
		},
	}
	cmd.SetVersionTemplate("hewitt {{.Version}}\n")
	cmd.CompletionOptions.DisableDefaultCmd = true
	cmd.PersistentFlags().StringVar(&configPath, "config", "", "configuration file (default: OS user config directory/hewitt/config.json)")
	return cmd
}
