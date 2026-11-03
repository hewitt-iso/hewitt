package cmd

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/hewitt-iso/hewitt/config"
	"github.com/hewitt-iso/hewitt/session"
	"github.com/spf13/cobra"
)

func newSessionCommands(cfg *config.Config) []*cobra.Command {
	withStore := func(run func(ctx context.Context, store *session.Store, id string, out io.Writer) error) func(*cobra.Command, []string) error {
		return func(cmd *cobra.Command, args []string) error {
			store, err := session.NewStore(cfg.DataDir)
			if err != nil {
				return err
			}
			return run(cmd.Context(), store, args[0], cmd.OutOrStdout())
		}
	}
	implemented := []*cobra.Command{
		{
			Use:     "stop <session>",
			Short:   "Stop a session's agent, keeping its container and workspace",
			Example: "  hewitt stop <session>",
			Args:    sessionArg,
			RunE: withStore(func(ctx context.Context, store *session.Store, id string, out io.Writer) error {
				resolved, err := session.Stop(ctx, store, id)
				if err != nil {
					return err
				}
				_, err = fmt.Fprintln(out, resolved)
				return err
			}),
		},
		{
			Use:     "rm <session>",
			Short:   "Remove a stopped session; refuses to discard uncommitted or unreturned work",
			Example: "  hewitt rm <session>",
			Args:    sessionArg,
			RunE: withStore(func(ctx context.Context, store *session.Store, id string, out io.Writer) error {
				resolved, err := session.Remove(ctx, store, id)
				if err != nil {
					return err
				}
				_, err = fmt.Fprintln(out, resolved)
				return err
			}),
		},
		{
			Use:     "diff <session>",
			Short:   "Show a session's changes since its workspace was created",
			Example: "  hewitt diff <session>",
			Args:    sessionArg,
			RunE:    withStore(session.Diff),
		},
	}
	commands := []*cobra.Command{
		{
			Use:     "logs <session>",
			Short:   "Read session output",
			Example: "  hewitt logs session-id",
			Args:    sessionArg,
		},
	}
	for _, cmd := range commands {
		if cmd.Long == "" {
			cmd.Long = cmd.Short + "."
		}
		cmd.Short += " (not implemented)"
		cmd.Long += "\n\nNot implemented yet. This command validates arguments but performs no session operations."
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("%s is not implemented yet", cmd.CommandPath())
		}
	}
	return append(implemented, commands...)
}

func sessionArg(cmd *cobra.Command, args []string) error {
	if err := cobra.ExactArgs(1)(cmd, args); err != nil {
		return err
	}
	if strings.TrimSpace(args[0]) == "" {
		return fmt.Errorf("session must not be empty")
	}
	return nil
}
