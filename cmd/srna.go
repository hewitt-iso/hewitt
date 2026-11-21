package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hewitt-iso/hewitt/config"
	"github.com/hewitt-iso/hewitt/session"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// newSRNAPromptCommand runs inside the tmux popup that a launch opens over its
// agent's pane. It asks about each pending request of that launch in turn and
// exits when none is left or the user postpones the rest. It is hidden: Hewitt
// starts it with the data directory, so it loads no configuration.
func newSRNAPromptCommand() *cobra.Command {
	var dataDir, sessionID, run string
	var unattributed bool
	cmd := &cobra.Command{
		Use:    "srna-prompt",
		Short:  "Ask about a launch's suspicious requests (started by Hewitt in a tmux popup)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !filepath.IsAbs(dataDir) {
				return fmt.Errorf("--data-dir must be an absolute path")
			}
			release, err := session.PopupLock(dataDir, run)
			if err != nil {
				return nil // another prompt of this launch is already open
			}
			defer release()
			store, err := session.NewStore(dataDir)
			if err != nil {
				return err
			}
			out, in := cmd.OutOrStdout(), cmd.InOrStdin()
			later := map[string]bool{}
			for {
				pending, err := store.PendingFor(sessionID, run, unattributed)
				if err != nil {
					return err
				}
				var r *session.SessionRequest
				for i := range pending {
					if !later[pending[i].ID] {
						r = &pending[i]
						break
					}
				}
				if r == nil {
					return nil
				}
				showRequest(out, *r, len(pending))
				switch key, err := readKey(in); {
				case err != nil:
					return err
				case key == 'a':
					err = store.Decide(*r, true)
				case key == 'd':
					err = store.Decide(*r, false)
				case key == 'k':
					err = store.Abort(cmd.Context(), *r)
				default:
					later[r.ID] = true
				}
				if err != nil {
					fmt.Fprintf(out, "\r\n %sError:%s %v\r\n Press any key.", ansiBoldRed, ansiReset, err)
					if _, err := readKey(in); err != nil {
						return err
					}
				}
			}
		},
	}
	cmd.Flags().StringVar(&dataDir, "data-dir", "", "Hewitt data directory")
	cmd.Flags().StringVar(&sessionID, "session", "", "session ID")
	cmd.Flags().StringVar(&run, "run", "", "agent launch ID")
	cmd.Flags().BoolVar(&unattributed, "unattributed", false, "also ask about requests that name no launch")
	return cmd
}

// showRequest draws one request: who asks, for what, and why it was held.
func showRequest(w io.Writer, r session.SessionRequest, pending int) {
	who := r.Agent
	if who == "" {
		who = "an unattributed process"
	}
	why := "a host outside the allowlist"
	if r.Kind == "file" {
		why = "a credential file in the workspace"
		if owner, ok := strings.CutPrefix(r.Target, "/hewitt/profiles/"); ok {
			owner, _, _ = strings.Cut(owner, "/")
			why = "another agent's login and settings (" + owner + ")"
		}
	}
	waited := time.Since(r.Created).Round(time.Second)
	kill := "abort " + who
	if r.Agent == "" {
		kill = "abort (deny only)"
	}
	more := ""
	if pending > 1 {
		more = fmt.Sprintf("   %s%d pending%s", ansiDim, pending, ansiReset)
	}
	fmt.Fprintf(w, "\x1b[H\x1b[2J\r\n %s#%d%s  %s%s%s wants %s%s\r\n\r\n", ansiBoldCyan, r.Number, ansiReset, ansiBold, who, ansiReset, r.Kind, more)
	fmt.Fprintf(w, "   %s\r\n", r.Target)
	fmt.Fprintf(w, "   %s%s · session %s%s\r\n", ansiDim, why, r.Session, ansiReset)
	fmt.Fprintf(w, "   %swaiting %s; refused after about a minute without a decision%s\r\n\r\n", ansiDim, waited, ansiReset)
	fmt.Fprintf(w, " %s[a]%s approve   %s[d]%s deny   %s[k]%s %s   %s[esc]%s later\r\n",
		ansiBoldCyan, ansiReset, ansiBoldCyan, ansiReset, ansiBoldRed, ansiReset, kill, ansiDim, ansiReset)
}

// readKey reads one key press, or the first character of a line when input
// is not a terminal. Escape sequences such as arrow keys count as escape.
func readKey(in io.Reader) (byte, error) {
	if f, ok := in.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		state, err := term.MakeRaw(int(f.Fd()))
		if err != nil {
			return 0, fmt.Errorf("prepare terminal: %w", err)
		}
		defer term.Restore(int(f.Fd()), state)
		buf := make([]byte, 8)
		n, err := f.Read(buf)
		if err != nil || n == 0 {
			return 0, err
		}
		if n > 1 {
			return 0x1b, nil
		}
		return lower(buf[0]), nil
	}
	line, err := bufio.NewReader(in).ReadString('\n')
	if line == "" {
		return 0, err
	}
	return lower(line[0]), nil
}

func lower(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + 'a' - 'A'
	}
	return b
}

// newStatusCommand summarizes what needs the user's attention, in a form that
// a tmux status line can show with #(hewitt status --tmux).
func newStatusCommand(cfg *config.Config) *cobra.Command {
	var tmux bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show what needs your attention, such as suspicious requests",
		Long: "Show what needs your attention, such as suspicious requests that need approval (SRNA).\n\n" +
			"--tmux prints a short status line segment, or nothing; add it to tmux with:\n" +
			"  set -g status-right '#(hewitt status --tmux) %H:%M'\n  set -g status-interval 5",
		Example: "  hewitt status\n  hewitt status --tmux",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := session.NewStore(cfg.DataDir)
			var list []session.SessionRequest
			if err == nil {
				list, err = store.Requests()
			}
			if err != nil {
				if tmux {
					return nil // a status line shows nothing rather than an error
				}
				return err
			}
			pending := map[string]int{}
			var sessions []string
			for _, r := range list {
				if r.Decision == "" {
					if pending[r.Session] == 0 {
						sessions = append(sessions, r.Session)
					}
					pending[r.Session]++
				}
			}
			total := 0
			for _, n := range pending {
				total += n
			}
			out := cmd.OutOrStdout()
			if tmux {
				if total > 0 {
					_, err = fmt.Fprintf(out, "#[fg=colour231,bg=colour160,bold] SRNA %d #[default]\n", total)
				}
				return err
			}
			if total == 0 {
				_, err = fmt.Fprintln(out, "No suspicious requests need approval.")
				return err
			}
			for _, name := range sessions {
				if _, err := fmt.Fprintf(out, "%s: %d suspicious request(s) need approval\n", name, pending[name]); err != nil {
					return err
				}
			}
			_, err = fmt.Fprintln(out, "hewitt requests lists them; hewitt approve, deny, or abort <numbers> decides.")
			return err
		},
	}
	cmd.Flags().BoolVar(&tmux, "tmux", false, "print a tmux status line segment, or nothing when nothing is pending")
	return cmd
}
