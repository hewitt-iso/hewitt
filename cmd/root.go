// Package cmd defines Hewitt's command-line interface.
package cmd

import (
	"github.com/hewitt-iso/hewitt/config"
	"github.com/spf13/cobra"
)

// NewRootCommand returns an independent command tree for one invocation.
func NewRootCommand(version string) *cobra.Command {
	var configPath string
	var cfg config.Config
	cmd := &cobra.Command{
		Use:           "hewitt",
		Short:         "Run coding agents in isolated local workspaces",
		Long:          "Hewitt is a local-first CLI for coding agents in Docker containers and Git worktrees.\n\nThe run command creates and starts a container from an explicit image.\nThe ps command lists saved sessions without requiring Docker.\nClaude/Codex adapters and subscription login use persistent native profiles.\nWorktrees, reconnectable terminal sessions, and the other session commands are not implemented yet.",
		Version:       version,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// Help/version flags bypass hooks; Cobra's help command does not.
			// Keep both help forms available when configuration is broken.
			if cmd.Name() == "help" {
				return nil
			}
			loaded, err := config.Load(configPath)
			cfg = loaded
			return err
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	cmd.SetVersionTemplate("hewitt {{.Version}}\n")
	cmd.CompletionOptions.DisableDefaultCmd = true
	cmd.PersistentFlags().StringVar(&configPath, "config", "", "configuration file (default: OS user config directory/hewitt/config.json)")
	cmd.AddCommand(newSessionCommands()...)
	cmd.AddCommand(newAgentCommand("run", &cfg), newAgentCommand("login", &cfg))
	cmd.AddCommand(newPSCommand(&cfg))
	return cmd
}
