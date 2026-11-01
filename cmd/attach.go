package cmd

import (
	"github.com/hewitt-iso/hewitt/config"
	"github.com/hewitt-iso/hewitt/session"
	"github.com/spf13/cobra"
)

func newAttachCommand(cfg *config.Config) *cobra.Command {
	return &cobra.Command{
		Use:   "attach <session>",
		Short: "Attach to a session's container terminal",
		Long: "Connect input and output to the existing process in a running session container.\n" +
			"Use the full session ID from hewitt ps. An interactive terminal and Docker\n" +
			"are required for interactive input. Press Ctrl-P then Ctrl-Q to detach and\n" +
			"leave the container running. Ctrl-C may interrupt the container process.\n\n" +
			"Does not start stopped containers, launch a shell, or replay previous output.\n" +
			"Docker errors if the container is stopped, missing, or unavailable.",
		Example: "  hewitt attach <full-session-id>",
		Args:    sessionArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := session.NewStore(cfg.DataDir)
			if err != nil {
				return err
			}
			return session.Attach(cmd.Context(), store, args[0], cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
}
