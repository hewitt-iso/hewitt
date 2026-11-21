package cmd

import (
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/hewitt-iso/hewitt/config"
	"github.com/hewitt-iso/hewitt/session"
	"github.com/spf13/cobra"
)

// Restricted sessions hold connections to hosts outside their allowlist as
// suspicious requests that need approval (SRNA). These commands list and
// decide them; the waiting connection proceeds or fails at once.

func newRequestsCommand(cfg *config.Config) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "requests",
		Short: "List suspicious requests that need approval (SRNA)",
		Long: "List suspicious requests that need approval (SRNA).\n\n" +
			"Restricted and hardened sessions hold connections to hosts outside their allowlist until you decide.\n" +
			"A connection waits about a minute; an agent that retries later proceeds once the request is approved.",
		Example: "  hewitt requests\n  hewitt requests -a\n  hewitt approve 3f9c2a1b7d04",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := session.NewStore(cfg.DataDir)
			if err != nil {
				return err
			}
			list, err := store.Requests()
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			if _, err := fmt.Fprintln(w, "ID\tSESSION\tKIND\tTARGET\tSTATUS\tCREATED"); err != nil {
				return fmt.Errorf("write request list: %w", err)
			}
			for _, r := range list {
				status := r.Decision
				if status == "" {
					status = "SRNA"
				} else if !all {
					continue
				}
				if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", r.ID, r.Session, r.Kind, r.Target, status, r.Created.UTC().Format(time.RFC3339)); err != nil {
					return fmt.Errorf("write request list: %w", err)
				}
			}
			if err := w.Flush(); err != nil {
				return fmt.Errorf("write request list: %w", err)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&all, "all", "a", false, "include decided requests")
	return cmd
}

func newDecideCommand(cfg *config.Config, allow bool) *cobra.Command {
	use, short, verb := "deny <request>", "Deny a suspicious request", "Denied"
	if allow {
		use, short, verb = "approve <request>", "Approve a suspicious request; later connections to the target pass", "Approved"
	}
	return &cobra.Command{
		Use:     use,
		Short:   short,
		Example: "  hewitt requests\n  hewitt " + use[:len(use)-len(" <request>")] + " 3f9c2a1b7d04",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := session.NewStore(cfg.DataDir)
			if err != nil {
				return err
			}
			r, err := store.Decide(args[0], allow)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s %s %s for session %s\n", verb, r.Kind, r.Target, r.Session)
			return err
		},
	}
}
