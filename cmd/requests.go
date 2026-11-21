package cmd

import (
	"errors"
	"fmt"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/hewitt-iso/hewitt/config"
	"github.com/hewitt-iso/hewitt/session"
	"github.com/spf13/cobra"
)

// Hewitt holds suspicious requests that need approval (SRNA): connections of
// restricted sessions to hosts outside their allowlist, and agents opening
// another agent's login or credential files. These commands list and decide
// them; the waiting connection or file open proceeds or fails at once.

func newRequestsCommand(cfg *config.Config) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "requests",
		Short: "List suspicious requests that need approval (SRNA)",
		Long: "List suspicious requests that need approval (SRNA).\n\n" +
			"Restricted and hardened sessions hold connections to hosts outside their allowlist, and the observer\n" +
			"holds an agent's access to another agent's logins or to credential files, until you decide.\n" +
			"A waiting request is refused after about a minute; an agent that retries proceeds once approved.\n" +
			"Numbers never change, so decide by the numbers shown: hewitt approve 1-3, hewitt deny 6-9, hewitt abort 4 5.",
		Example: "  hewitt requests\n  hewitt requests -a\n  hewitt approve 1-3 7\n  hewitt abort 4 5",
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
			if _, err := fmt.Fprintln(w, "#\tSESSION\tAGENT\tKIND\tTARGET\tSTATUS\tCREATED"); err != nil {
				return fmt.Errorf("write request list: %w", err)
			}
			for _, r := range list {
				status := r.Decision
				if status == "" {
					status = "SRNA"
				} else if !all {
					continue
				}
				agent := r.Agent
				if agent == "" {
					agent = "-"
				}
				if _, err := fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\t%s\n", r.Number, r.Session, agent, r.Kind, r.Target, status, r.Created.UTC().Format(time.RFC3339)); err != nil {
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

// newDecideCommand decides a batch of requests: approve or deny remember the
// decision for later requests to the same target; abort also kills the agent
// connection that made each request.
func newDecideCommand(cfg *config.Config, action string) *cobra.Command {
	short := map[string]string{
		"approve": "Approve suspicious requests; later requests to the same target pass",
		"deny":    "Deny suspicious requests; later requests to the same target are refused",
		"abort":   "Deny suspicious requests and kill the agents that made them",
	}[action]
	verb := map[string]string{"approve": "Approved", "deny": "Denied", "abort": "Aborted"}[action]
	return &cobra.Command{
		Use:     action + " <number|range|id>...",
		Short:   short,
		Long:    short + ".\n\nSelect requests by the numbers hewitt requests shows: single numbers, ranges such as 3-7, or\ncomma-separated lists; shell braces such as {1..3} also work. Every selected request must exist.",
		Example: "  hewitt " + action + " 3\n  hewitt " + action + " 1-3 7\n  hewitt " + action + " {6..9}",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := session.NewStore(cfg.DataDir)
			if err != nil {
				return err
			}
			selected, err := store.SelectRequests(args)
			if err != nil {
				return err
			}
			var failures []error
			for _, r := range selected {
				switch action {
				case "approve":
					err = store.Decide(r, true)
				case "deny":
					err = store.Decide(r, false)
				case "abort":
					err = store.Abort(cmd.Context(), r)
				}
				if err != nil {
					failures = append(failures, err)
					continue
				}
				agent := ""
				if r.Agent != "" {
					agent = " (" + r.Agent + ")"
				}
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s #%s %s %s in %s%s\n", verb, strconv.Itoa(r.Number), r.Kind, r.Target, r.Session, agent); err != nil {
					return err
				}
			}
			return errors.Join(failures...)
		},
	}
}
