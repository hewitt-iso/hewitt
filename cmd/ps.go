package cmd

import (
	"fmt"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/hewitt-iso/hewitt/config"
	"github.com/hewitt-iso/hewitt/session"
	"github.com/spf13/cobra"
)

func newPSCommand(cfg *config.Config) *cobra.Command {
	var all, noTrunc bool
	cmd := &cobra.Command{
		Use:     "ps",
		Short:   "List saved sessions",
		Example: "  hewitt ps\n  hewitt ps -a\n  hewitt ps --no-trunc\n  hewitt ps --config /path/to/config.json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := session.NewStore(cfg.DataDir)
			if err != nil {
				return err
			}
			sessions, err := store.List()
			if err != nil {
				return err
			}
			// Shared workspaces list their connected agents rather than "workspace".
			runs, err := store.RunningAgents()
			if err != nil {
				return err
			}
			connected := map[string][]string{}
			for _, run := range runs {
				connected[run.WorkspaceID] = append(connected[run.WorkspaceID], run.Agent)
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			if _, err := fmt.Fprintln(w, "ID\tAGENT\tIMAGE\tNAME\tSTATUS\tCREATED"); err != nil {
				return fmt.Errorf("write session list: %w", err)
			}
			for _, m := range sessions {
				status := session.Status(cmd.Context(), m)
				if !all && status == "stopped" {
					continue
				}
				agents := m.Agent
				if m.Terminal == "host" {
					agents = "-"
					if names := connected[m.ID]; len(names) > 0 {
						slices.Sort(names)
						agents = strings.Join(names, ",")
					} else if status == "running" {
						status = "idle"
					}
				}
				id := shortID(m.ID)
				if noTrunc {
					id = m.ID
				}
				name := m.Name
				if name == "" {
					name = "-"
				}
				if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", id, agents, m.Image, name, status, m.CreatedAt.UTC().Format(time.RFC3339)); err != nil {
					return fmt.Errorf("write session list: %w", err)
				}
			}
			if err := w.Flush(); err != nil {
				return fmt.Errorf("write session list: %w", err)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&all, "all", "a", false, "show all sessions (default hides stopped sessions)")
	cmd.Flags().BoolVar(&noTrunc, "no-trunc", false, "do not truncate session IDs")
	return cmd
}
