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
	return &cobra.Command{
		Use:   "ps",
		Short: "List saved sessions",
		Long: "List all saved sessions from the configured data_dir in session ID order.\n" +
			"Shows full IDs, agents, images, names, and creation times in UTC.\n" +
			"An empty store prints only the table header. No files are created.\n\n" +
			"Docker is not required or queried. Records describe creation intent, not live\n" +
			"container status or agent readiness. Sessions remain listed after failed starts\n" +
			"or external container removal until their metadata is deleted.",
		Example: "  hewitt ps\n  hewitt ps --config /path/to/config.json",
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
			if _, err := fmt.Fprintln(w, "ID\tAGENT\tIMAGE\tNAME\tCREATED"); err != nil {
				return fmt.Errorf("write session list: %w", err)
			}
			for _, m := range sessions {
				name := m.Name
				if name == "" {
					name = "-"
				}
				if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", m.ID, m.Agent, m.Image, name, m.CreatedAt.UTC().Format(time.RFC3339)); err != nil {
					return fmt.Errorf("write session list: %w", err)
				}
			}
			if err := w.Flush(); err != nil {
				return fmt.Errorf("write session list: %w", err)
			}
			return nil
		},
	}
}
