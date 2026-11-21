package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hewitt-iso/hewitt/config"
	"github.com/hewitt-iso/hewitt/session"
	"github.com/spf13/cobra"
)

func newTrustCommand(cfg *config.Config) *cobra.Command {
	var repository, file string
	cmd := &cobra.Command{
		Use:   "trust",
		Short: "Allow a reviewed hewitt.yml to apply its sensitive settings",
		Long: "Allow a reviewed hewitt.yml to apply its sensitive settings.\n\n" +
			"A hewitt.yml arrives with the repository. Settings that could expose agent logins or host files\n" +
			"(image, danger, host paths outside the file's directory, network, ssh, egress, telemetry, exec and sidecar modes)\n" +
			"apply only after the file is trusted. Trust covers the current contents; any change needs trust again.",
		Example: "  hewitt trust\n  hewitt trust --file ../shared/hewitt.yml",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, err := filepath.Abs(repository)
			if err != nil {
				return fmt.Errorf("locate repository: %w", err)
			}
			p, err := config.LoadProject(file, repo)
			if err != nil {
				return err
			}
			if p.Path == "" {
				return fmt.Errorf("no %s or %s in %s", config.ProjectFile, config.ProjectFileAlt, repo)
			}
			keys := p.Sensitive()
			if len(keys) == 0 {
				_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s has no sensitive settings; it needs no trust\n", p.Path)
				return err
			}
			if err := session.TrustProject(cfg.DataDir, p.Path, p.Digest); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Trusted %s: %s\n", p.Path, strings.Join(keys, ", "))
			return err
		},
	}
	cmd.Flags().StringVar(&repository, "repo", ".", "repository containing hewitt.yml")
	cmd.Flags().StringVar(&file, "file", "", "project file to trust (default: hewitt.yml in --repo)")
	return cmd
}

// confirmTrust shows an untrusted hewitt.yml and asks whether to trust it. It
// trusts exactly the contents shown; a file changed meanwhile is refused.
func confirmTrust(cmd *cobra.Command, dataDir string, untrusted *session.UntrustedProjectError) error {
	data, err := os.ReadFile(untrusted.Path)
	if err != nil {
		return fmt.Errorf("read %s: %w", untrusted.Path, err)
	}
	if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != untrusted.Digest {
		return fmt.Errorf("%s changed while it was being reviewed: %w", untrusted.Path, untrusted)
	}
	stderr := cmd.ErrOrStderr()
	if _, err := fmt.Fprintf(stderr, "%s%s%s sets %s, which can expose agent logins or host files:\r\n\r\n", ansiBoldCyan, untrusted.Path, ansiReset, strings.Join(untrusted.Keys, ", ")); err != nil {
		return err
	}
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if _, err := fmt.Fprintf(stderr, "  %s%s%s\r\n", ansiDim, line, ansiReset); err != nil {
			return err
		}
	}
	_, _ = fmt.Fprint(stderr, "\r\n")
	choice, err := pickOption(cmd.InOrStdin(), stderr, "Trust this file?", []string{
		"trust    (apply these settings; asked again when the file changes)",
		"cancel",
	}, "trust cancelled", "write trust prompt")
	if err != nil {
		return err
	}
	_, _ = fmt.Fprint(stderr, "\r\n")
	if choice != 0 {
		return untrusted
	}
	return session.TrustProject(dataDir, untrusted.Path, untrusted.Digest)
}

// trustAndRetry asks to trust an untrusted hewitt.yml in a terminal and then
// repeats the operation once; other errors pass through.
func trustAndRetry[T any](cmd *cobra.Command, dataDir string, run func() (T, error)) (T, error) {
	result, err := run()
	var untrusted *session.UntrustedProjectError
	if !errors.As(err, &untrusted) || !isInteractive(cmd) {
		return result, err
	}
	if err := confirmTrust(cmd, dataDir, untrusted); err != nil {
		return result, err
	}
	return run()
}
