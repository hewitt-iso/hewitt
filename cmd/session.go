package cmd

import (
	"context"
	"fmt"
	"io"
	"slices"
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
	var force bool
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
				if _, err := fmt.Fprintln(out, resolved); err != nil {
					return err
				}
				if m, err := store.Get(resolved); err != nil || m.Volume == nil {
					return err
				}
				branches, err := session.Sync(ctx, store, resolved)
				if err != nil {
					return fmt.Errorf("session stopped; sync its branches with hewitt sync %s: %w", resolved, err)
				}
				return printBranches(out, branches)
			}),
		},
		{
			Use:               "sync [session]",
			Short:             "Import a session's branches into the host repository",
			Long:              "Import a session's branches into the host repository as hewitt/<name>/<branch>.\n\nThe session repository lives in a Docker volume; a helper container exports\nits branches as a Git bundle and the host fetches it, so no hook or\nconfiguration from the session repository runs on the host. Works on running\nand stopped sessions.",
			Example:           "  hewitt sync claude-1\n  hewitt sync",
			Args:              optionalSessionArg,
			ValidArgsFunction: completeSessions(cfg, nil),
			RunE: withStore("Select a session to sync", "no sessions found", nil, func(ctx context.Context, store *session.Store, id string, out io.Writer) error {
				branches, err := session.Sync(ctx, store, id)
				if err != nil {
					return err
				}
				return printBranches(out, branches)
			}),
		},
		{
			Use:               "rm [session]",
			Short:             "Remove a stopped session and all its resources",
			Example:           "  hewitt rm <session>\n  hewitt rm",
			Args:              optionalSessionArg,
			ValidArgsFunction: completeSessions(cfg, notRunning),
			RunE: withStore("Select a stopped session to remove", "no stopped sessions found", notRunning, func(ctx context.Context, store *session.Store, id string, out io.Writer) error {
				resolved, err := session.Remove(ctx, store, id, force)
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
	implemented[2].Flags().BoolVar(&force, "force", false, "discard uncommitted work in the session volume")
	return append(implemented, commands...)
}

// printBranches lists updated host branches, or says that none changed.
func printBranches(out io.Writer, branches []string) error {
	if len(branches) == 0 {
		_, err := fmt.Fprintln(out, "No new commits to import.")
		return err
	}
	slices.Sort(branches)
	for _, branch := range branches {
		if _, err := fmt.Fprintln(out, "Imported", branch); err != nil {
			return err
		}
	}
	return nil
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
