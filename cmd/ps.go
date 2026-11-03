package cmd

import (
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/hewitt-iso/hewitt/config"
	"github.com/hewitt-iso/hewitt/session"
	"github.com/spf13/cobra"
)

func newPSCommand(cfg *config.Config) *cobra.Command {
	var all, noTrunc bool
	cmd := &cobra.Command{
		Use:   "ps",
		Short: "List saved sessions",
		Long: "List saved sessions from the configured data_dir in session ID order.\n" +
			"By default, stopped sessions are omitted; use --all (-a) to include them.\n" +
			"Shows 12-character short IDs (or full IDs with --no-trunc), agents, images,\n" +
			"names, status, and creation times in UTC.\n" +
			"An empty store prints only the table header. No files are created.\n\n" +
			"Docker is not required; when Docker is unavailable, status is reported as\n" +
			"unknown and all saved sessions are listed.",
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
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			if _, err := fmt.Fprintln(w, "ID\tAGENT\tIMAGE\tNAME\tSTATUS\tCREATED"); err != nil {
				return fmt.Errorf("write session list: %w", err)
			}
			for _, m := range sessions {
				status := session.Status(cmd.Context(), m)
				if !all && status == "stopped" {
					continue
				}
				id := shortID(m.ID)
				if noTrunc {
					id = m.ID
				}
				name := m.Name
				if name == "" {
					name = "-"
				}
				if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", id, m.Agent, m.Image, name, status, m.CreatedAt.UTC().Format(time.RFC3339)); err != nil {
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
