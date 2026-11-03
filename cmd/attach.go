package cmd

import (
	"context"

	"github.com/hewitt-iso/hewitt/config"
	"github.com/hewitt-iso/hewitt/session"
	"github.com/spf13/cobra"
)

func newAttachCommand(cfg *config.Config) *cobra.Command {
	notStopped := func(_ context.Context, _ session.Metadata, status string) bool {
		return status != "stopped"
	}
	return &cobra.Command{
		Use:               "attach [session]",
		Short:             "Attach to a session's container terminal",
		Example:           "  hewitt attach <session>\n  hewitt attach",
		Args:              optionalSessionArg,
		ValidArgsFunction: completeSessions(cfg, notStopped),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := session.NewStore(cfg.DataDir)
			if err != nil {
				return err
			}
			ref, err := resolveOrPickSession(cmd, store, args, "Select a session to attach", notStopped, "no running sessions found")
			if err != nil {
				return err
			}
			return session.Attach(cmd.Context(), store, ref, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
}
