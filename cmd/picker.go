package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/hewitt-iso/hewitt/config"
	"github.com/hewitt-iso/hewitt/session"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

const shortIDLen = 12

func shortID(id string) string {
	if len(id) > shortIDLen {
		return id[:shortIDLen]
	}
	return id
}

// terminalReader allows tests to opt into interactive terminal prompts without
// requiring a hardware PTY.
type terminalReader interface {
	io.Reader
	isTerminal() bool
}

func isInteractive(cmd *cobra.Command) bool {
	in := cmd.InOrStdin()
	if tr, ok := in.(terminalReader); ok {
		return tr.isTerminal()
	}
	inFile, ok := in.(*os.File)
	if !ok || !term.IsTerminal(int(inFile.Fd())) {
		return false
	}
	errFile, ok := cmd.ErrOrStderr().(*os.File)
	return ok && term.IsTerminal(int(errFile.Fd()))
}

func optionalSessionArg(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		if isInteractive(cmd) {
			return nil
		}
		return fmt.Errorf("accepts 1 arg(s), received 0")
	}
	if err := cobra.ExactArgs(1)(cmd, args); err != nil {
		return err
	}
	if strings.TrimSpace(args[0]) == "" {
		return fmt.Errorf("session must not be empty")
	}
	return nil
}

type sessionCandidate struct {
	meta   session.Metadata
	status string
}

func resolveOrPickSession(cmd *cobra.Command, store *session.Store, args []string, prompt string, filter func(ctx context.Context, m session.Metadata, status string) bool, emptyMsg string) (string, error) {
	if len(args) > 0 {
		return args[0], nil
	}
	sessions, err := store.List()
	if err != nil {
		return "", err
	}
	ctx := cmd.Context()
	var candidates []sessionCandidate
	for _, m := range sessions {
		status := session.Status(ctx, m)
		if filter == nil || filter(ctx, m, status) {
			candidates = append(candidates, sessionCandidate{meta: m, status: status})
		}
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("%s", emptyMsg)
	}
	return pickSession(cmd.InOrStdin(), cmd.ErrOrStderr(), candidates, prompt)
}

func pickSession(in io.Reader, stderr io.Writer, candidates []sessionCandidate, prompt string) (string, error) {
	if f, ok := in.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		fd := int(f.Fd())
		state, err := term.MakeRaw(fd)
		if err != nil {
			return "", fmt.Errorf("prepare terminal for selection: %w", err)
		}
		defer term.Restore(fd, state)
	}

	selected := 0
	render := func(redraw bool) error {
		if redraw {
			if _, err := fmt.Fprintf(stderr, "\x1b[%dA\r\x1b[J", len(candidates)+1); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(stderr, "%s (↑/↓ or j/k to move, enter to select, q to cancel):\r\n", prompt); err != nil {
			return err
		}
		for i, c := range candidates {
			cursor := " "
			if i == selected {
				cursor = ">"
			}
			name := c.meta.Name
			if name == "" {
				name = "-"
			}
			if _, err := fmt.Fprintf(stderr, "%s %d) %s  %-8s  %-16s  %-8s  %s\r\n",
				cursor, i+1, shortID(c.meta.ID), c.meta.Agent, name, c.status, c.meta.CreatedAt.UTC().Format(time.RFC3339)); err != nil {
				return err
			}
		}
		return nil
	}

	if err := render(false); err != nil {
		return "", fmt.Errorf("write session picker: %w", err)
	}

	var buf [3]byte
	for {
		n, err := in.Read(buf[:1])
		if n == 0 {
			if err != nil {
				return "", fmt.Errorf("session selection cancelled")
			}
			continue
		}
		b := buf[0]
		switch {
		case b == '\r' || b == '\n':
			return candidates[selected].meta.ID, nil
		case b == 'q' || b == 'Q' || b == 3 || b == 4:
			return "", fmt.Errorf("session selection cancelled")
		case b == 'k' || b == 'K':
			if selected > 0 {
				selected--
				if err := render(true); err != nil {
					return "", fmt.Errorf("write session picker: %w", err)
				}
			}
		case b == 'j' || b == 'J':
			if selected < len(candidates)-1 {
				selected++
				if err := render(true); err != nil {
					return "", fmt.Errorf("write session picker: %w", err)
				}
			}
		case b >= '1' && b <= '9':
			idx := int(b - '1')
			if idx < len(candidates) {
				selected = idx
				if err := render(true); err != nil {
					return "", fmt.Errorf("write session picker: %w", err)
				}
			}
		case b == 0x1b:
			// Read optional ANSI escape sequence for arrow keys.
			n, _ := in.Read(buf[1:3])
			if n == 2 && buf[1] == '[' {
				switch buf[2] {
				case 'A':
					if selected > 0 {
						selected--
						if err := render(true); err != nil {
							return "", fmt.Errorf("write session picker: %w", err)
						}
					}
				case 'B':
					if selected < len(candidates)-1 {
						selected++
						if err := render(true); err != nil {
							return "", fmt.Errorf("write session picker: %w", err)
						}
					}
				}
				continue
			}
			return "", fmt.Errorf("session selection cancelled")
		}
	}
}

func completeSessions(cfg *config.Config, filter func(ctx context.Context, m session.Metadata, status string) bool) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		dataDir := cfg.DataDir
		if dataDir == "" {
			var configPath string
			if f := cmd.Flags().Lookup("config"); f != nil {
				configPath = f.Value.String()
			}
			loaded, err := config.Load(configPath)
			if err != nil {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			dataDir = loaded.DataDir
		}
		store, err := session.NewStore(dataDir)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		sessions, err := store.List()
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		ctx := cmd.Context()
		var completions []string
		for _, m := range sessions {
			status := session.Status(ctx, m)
			if filter != nil && !filter(ctx, m, status) {
				continue
			}
			short := shortID(m.ID)
			if m.Name != "" && strings.HasPrefix(m.Name, toComplete) {
				completions = append(completions, fmt.Sprintf("%s\t%s (%s, %s)", m.Name, m.Agent, short, status))
			}
			if strings.HasPrefix(m.ID, toComplete) {
				desc := fmt.Sprintf("%s (%s)", m.Agent, status)
				if m.Name != "" {
					desc = fmt.Sprintf("%s (%s, %s)", m.Agent, m.Name, status)
				}
				completions = append(completions, fmt.Sprintf("%s\t%s", short, desc))
			}
		}
		return completions, cobra.ShellCompDirectiveNoFileComp
	}
}
