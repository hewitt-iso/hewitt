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
