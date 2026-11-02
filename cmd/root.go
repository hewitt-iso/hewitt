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
	auth := &cobra.Command{Use: "auth", Short: "Manage native agent authentication", Args: cobra.NoArgs}
	auth.AddCommand(newAgentCommand("login", &cfg), newAgentCommand("status", &cfg), newAgentCommand("logout", &cfg))
	cmd.AddCommand(auth, newPSCommand(&cfg), newAttachCommand(&cfg))
	return cmd
}
