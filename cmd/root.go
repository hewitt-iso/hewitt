// Package cmd defines Hewitt's command-line interface.
package cmd

import (
	"fmt"

	"github.com/hewitt-iso/hewitt/config"
	"github.com/hewitt-iso/hewitt/profile"
	"github.com/hewitt-iso/hewitt/session"
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
			// Help/version/completion bypass configuration loading so they stay
			// available when configuration is missing or broken.
			if cmd.Name() == "help" || cmd.Name() == "completion" || (cmd.Parent() != nil && cmd.Parent().Name() == "completion") {
				return nil
			}
			loaded, err := config.Load(configPath)
			cfg = loaded
			return err
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if !isInteractive(cmd) {
				return cmd.Help()
			}
			opts := session.AgentOptions{DataDir: cfg.DataDir, Repository: "."}
			if !profile.IsOnboarded(cfg.DataDir) {
				start, err := runOnboarding(cmd, &cfg, &opts)
				if err != nil {
					return err
				}
				if !start {
					_, err := fmt.Fprintf(cmd.ErrOrStderr(), "Onboarding complete. Run %shewitt%s to select an agent.\n", ansiBoldCyan, ansiReset)
					return err
				}
			}
			return executeRun(cmd, &cfg, opts, false)
		},
	}
	cmd.SetVersionTemplate("hewitt {{.Version}}\n")
	cmd.PersistentFlags().StringVar(&configPath, "config", "", "configuration file (default: OS user config directory/hewitt/config.json)")
	cmd.AddCommand(newSessionCommands(&cfg)...)
	cmd.AddCommand(newShellCommand(&cfg), newAgentCommand("run", &cfg), newAgentCommand("login", &cfg))
	auth := &cobra.Command{Use: "auth", Short: "Manage native agent authentication", Args: cobra.NoArgs}
	auth.AddCommand(newAgentCommand("login", &cfg), newAgentCommand("status", &cfg), newAgentCommand("logout", &cfg))
	cmd.AddCommand(auth, newPSCommand(&cfg), newAttachCommand(&cfg))
	return cmd
}
