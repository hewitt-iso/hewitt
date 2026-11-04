package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/hewitt-iso/hewitt/config"
	"github.com/hewitt-iso/hewitt/session"
	"github.com/spf13/cobra"
)

func newAttachCommand(cfg *config.Config) *cobra.Command {
	notStopped := func(_ context.Context, _ session.Metadata, status string) bool {
		return status != "stopped"
	}
	return &cobra.Command{
		Use:     "attach [session]",
		Short:   "Return to a running agent in host tmux or a legacy terminal",
		Example: "  hewitt attach <session>\n  hewitt attach\n  hewitt attach <agent-run-id>",
		Args:    optionalSessionArg,
		ValidArgsFunction: func(cmd *cobra.Command, args []string, prefix string) ([]string, cobra.ShellCompDirective) {
			options, directive := completeSessions(cfg, notStopped)(cmd, args, prefix)
			if len(args) > 0 {
				return options, directive
			}
			dataDir := cfg.DataDir
			if dataDir == "" {
				path, _ := cmd.Flags().GetString("config")
				loaded, err := config.Load(path)
				if err != nil {
					return options, directive
				}
				dataDir = loaded.DataDir
			}
			store, err := session.NewStore(dataDir)
			if err != nil {
				return options, directive
			}
			runs, err := store.RunningAgents()
			if err != nil {
				return options, directive
			}
			for _, run := range runs {
				if strings.HasPrefix(run.ID, prefix) {
					options = append(options, fmt.Sprintf("%s\t%s in %s", shortID(run.ID), run.Agent, run.WorkspaceName))
				}
			}
			return options, directive
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := session.NewStore(cfg.DataDir)
			if err != nil {
				return err
			}
			runs, err := store.RunningAgents()
			if err != nil {
				return err
			}
			type target struct{ ref, label string }
			var targets []target
			workspaceID := ""
			if len(args) > 0 {
				var matches []session.AgentRun
				for _, run := range runs {
					if strings.HasPrefix(run.ID, args[0]) {
						matches = append(matches, run)
					}
				}
				if len(matches) > 1 {
					return fmt.Errorf("agent run %q is ambiguous; use a longer ID", args[0])
				}
				if len(matches) == 1 {
					return session.Attach(cmd.Context(), store, matches[0].ID, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
				}
				m, err := store.Resolve(args[0])
				if err != nil {
					return err
				}
				if m.Terminal != "host" {
					return session.Attach(cmd.Context(), store, m.ID, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
				}
				workspaceID = m.ID
			}
			for _, run := range runs {
				if workspaceID != "" && run.WorkspaceID != workspaceID {
					continue
				}
				location := "host tmux"
				if run.Terminal.Socket == "" {
					location = "plain terminal; cannot reattach"
				}
				targets = append(targets, target{run.ID, fmt.Sprintf("%s  %-12s  %s  (%s)", shortID(run.ID), run.Agent, run.WorkspaceName, location)})
			}
			if len(args) == 0 {
				sessions, err := store.List()
				if err != nil {
					return err
				}
				for _, m := range sessions {
					if m.Terminal == "host" {
						continue
					}
					if session.Status(cmd.Context(), m) == "stopped" {
						continue
					}
					targets = append(targets, target{m.ID, fmt.Sprintf("%s  %-12s  %s  (legacy terminal)", shortID(m.ID), m.Agent, m.Name)})
				}
			}
			if len(targets) == 0 {
				return fmt.Errorf("no running agents or legacy sessions found")
			}
			selected := 0
			if isInteractive(cmd) {
				options := make([]string, len(targets))
				for i, target := range targets {
					options[i] = target.label
				}
				selected, err = pickOption(cmd.InOrStdin(), cmd.ErrOrStderr(), "Select a running agent", options, "agent selection cancelled", "write agent picker")
				if err != nil {
					return err
				}
			} else if len(targets) > 1 {
				return fmt.Errorf("multiple agents are running; specify an agent run ID")
			}
			return session.Attach(cmd.Context(), store, targets[selected].ref, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
}
