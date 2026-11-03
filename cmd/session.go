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
	withStore := func(prompt, emptyMsg string, filter func(ctx context.Context, m session.Metadata, status string) bool, run func(ctx context.Context, store *session.Store, id string, out io.Writer) error) func(*cobra.Command, []string) error {
		return func(cmd *cobra.Command, args []string) error {
			store, err := session.NewStore(cfg.DataDir)
			if err != nil {
				return err
			}
			ref, err := resolveOrPickSession(cmd, store, args, prompt, filter, emptyMsg)
			if err != nil {
				return err
			}
			return run(cmd.Context(), store, ref, cmd.OutOrStdout())
		}
	}
	notStopped := func(_ context.Context, _ session.Metadata, status string) bool {
		return status != "stopped"
	}
	notRunning := func(_ context.Context, _ session.Metadata, status string) bool {
		return status != "running"
	}
	implemented := []*cobra.Command{
		{
			Use:               "stop [session]",
			Short:             "Stop a session's agent, keeping its container and workspace",
			Example:           "  hewitt stop <session>\n  hewitt stop",
			Args:              optionalSessionArg,
			ValidArgsFunction: completeSessions(cfg, notStopped),
			RunE: withStore("Select a session to stop", "no running sessions found", notStopped, func(ctx context.Context, store *session.Store, id string, out io.Writer) error {
				resolved, err := session.Stop(ctx, store, id)
				if err != nil {
					return err
				}
				_, err = fmt.Fprintln(out, resolved)
				return err
			}),
		},
		{
			Use:               "rm [session]",
			Short:             "Remove a stopped session and all its resources",
			Example:           "  hewitt rm <session>\n  hewitt rm",
			Args:              optionalSessionArg,
			ValidArgsFunction: completeSessions(cfg, notRunning),
			RunE: withStore("Select a stopped session to remove", "no stopped sessions found", notRunning, func(ctx context.Context, store *session.Store, id string, out io.Writer) error {
				resolved, err := session.Remove(ctx, store, id)
				if err != nil {
					return err
				}
				_, err = fmt.Fprintln(out, resolved)
				return err
			}),
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
