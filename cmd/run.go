package cmd

import (
	"fmt"

	"github.com/hewitt-iso/hewitt/config"
	"github.com/hewitt-iso/hewitt/session"
	"github.com/spf13/cobra"
)

func newAgentCommand(action string, cfg *config.Config) *cobra.Command {
	var image, name, profile string
	short := "Start an agent container with a persistent native profile"
	if action == "login" {
		short = "Log in to a subscription using the native agent in a container"
	}
	cmd := &cobra.Command{
		Use: action + " <agent>", Short: short,
		Long:      short + ".\n\nSupported adapters: claude, codex. Planned agents: opencode, gemini, copilot.\nThe image must contain env and the selected CLI on PATH and support the host UID/GID.\nA dedicated profile is mounted; no repository or host credentials are imported.\nLogin requires an interactive terminal. Containers and profiles are preserved.\nDo not use one profile concurrently. Worktrees and reconnectable sessions are not implemented.",
		Example:   "  hewitt " + action + " codex --image my-codex:local --profile personal",
		ValidArgs: []string{"claude", "codex", "opencode", "gemini", "copilot"},
		Args:      cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := session.AgentOptions{Agent: args[0], Image: image, Name: name, Profile: profile, DataDir: cfg.DataDir}
			if action == "login" {
				return session.Login(cmd.Context(), opts, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
			}
			id, err := session.RunAgent(cmd.Context(), opts)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), id)
			return err
		},
	}
	cmd.Flags().StringVar(&image, "image", "", "image containing the selected native CLI (required)")
	cmd.Flags().StringVar(&name, "name", "", "optional Docker container name")
	cmd.Flags().StringVar(&profile, "profile", "default", "persistent profile ID, scoped to the agent")
	if err := cmd.MarkFlagRequired("image"); err != nil {
		panic(err)
	}
	return cmd
}
