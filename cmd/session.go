package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func newSessionCommands() []*cobra.Command {
	commands := []*cobra.Command{
		{
			Use:       "run <agent>",
			Short:     "Start an agent in an isolated container and worktree",
			Long:      "Start a coding agent with its own Docker container, Git worktree, and terminal session.\n\nPlanned agents: claude (Claude Code), codex (Codex), opencode (OpenCode).",
			Example:   "  hewitt run claude\n  hewitt run codex\n  hewitt run opencode",
			ValidArgs: []string{"claude", "codex", "opencode"},
			Args:      cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		},
		{
			Use:     "ps",
			Short:   "List sessions",
			Example: "  hewitt ps",
			Args:    cobra.NoArgs,
		},
		{
			Use:     "attach <session>",
			Short:   "Reconnect to a session's terminal",
			Example: "  hewitt attach session-id",
			Args:    sessionArg,
		},
		{
			Use:     "stop <session>",
			Short:   "Stop a session",
			Example: "  hewitt stop session-id",
			Args:    sessionArg,
		},
		{
			Use:     "rm <session>",
			Short:   "Remove a session and clean up its resources",
			Example: "  hewitt rm session-id",
			Args:    sessionArg,
		},
		{
			Use:     "logs <session>",
			Short:   "Read session output",
			Example: "  hewitt logs session-id",
			Args:    sessionArg,
		},
		{
			Use:     "diff <session>",
			Short:   "Inspect a session's worktree changes",
			Example: "  hewitt diff session-id",
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
	return commands
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
