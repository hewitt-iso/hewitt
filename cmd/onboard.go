package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/hewitt-iso/hewitt/config"
	"github.com/hewitt-iso/hewitt/profile"
	"github.com/hewitt-iso/hewitt/session"
	"github.com/spf13/cobra"
)

// runOnboarding runs the 3-step first-time setup wizard when stdin is an
// interactive terminal and dataDir has not been onboarded yet:
//  1. Import configs (skills, MCP, tmux, rules)
//  2. Create new workspace?
//  3. Share credentials
func runOnboarding(cmd *cobra.Command, cfg *config.Config, opts *session.AgentOptions) (bool, error) {
	if !isInteractive(cmd) || profile.IsOnboarded(cfg.DataDir) {
		return true, nil
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return false, fmt.Errorf("locate home directory: %w", err)
	}
	profileID := opts.Profile
	if profileID == "" {
		profileID = "default"
	}
	in := cmd.InOrStdin()
	stderr := cmd.ErrOrStderr()

	// Step 1: Import configs (skills, MCP, tmux, rules).
	detectedConfigs := profile.DetectHostConfigs(homeDir)
	importCfgLabel := "Yes — import host configs (skills, MCP, tmux, rules)"
	if len(detectedConfigs) > 0 {
		importCfgLabel += " [" + strings.Join(detectedConfigs, ", ") + "]"
	}
	step1, err := pickOption(in, stderr, "[1/3] Import configs (skills, MCP, tmux, rules)?", []string{
		importCfgLabel,
		"No — start with a clean profile",
	}, "onboarding cancelled", "write onboarding prompt")
	if err != nil {
		return false, err
	}
	if step1 == 0 {
		imported, err := profile.ImportHostConfigs(cfg.DataDir, homeDir, profileID)
		if err != nil {
			return false, err
		}
		if len(imported) > 0 {
			if _, err := fmt.Fprintf(stderr, "Imported configs: %s\r\n", strings.Join(imported, ", ")); err != nil {
				return false, err
			}
		} else {
			if _, err := fmt.Fprintln(stderr, "No host configs found to import.\r"); err != nil {
				return false, err
			}
		}
	}

	// Step 2: Create new workspace?
	step2, err := pickOption(in, stderr, "[2/3] Create new workspace?", []string{
		"Yes — Git worktree (isolated branch, recommended)",
		"Yes — Copy workspace (isolated directory copy)",
		"No — finish onboarding without starting a workspace",
	}, "onboarding cancelled", "write onboarding prompt")
	if err != nil {
		return false, err
	}
	startWorkspace := step2 != 2
	if opts.Workspace == "" {
		switch step2 {
		case 0:
			opts.Workspace = "worktree"
		case 1:
			opts.Workspace = "copy"
		}
	}

	// Step 3: Share credentials.
	detectedCreds := profile.DetectHostCredentials(homeDir)
	shareCredLabel := "Yes — copy host credentials into Hewitt profile"
	if len(detectedCreds) > 0 {
		shareCredLabel += " [" + strings.Join(detectedCreds, ", ") + "]"
	}
	step3, err := pickOption(in, stderr, "[3/3] Share credentials?", []string{
		shareCredLabel,
		"Log in interactively now (hewitt login)",
		"Skip — configure credentials later",
	}, "onboarding cancelled", "write onboarding prompt")
	if err != nil {
		return false, err
	}
	switch step3 {
	case 0:
		imported, err := profile.ImportHostCredentials(cfg.DataDir, homeDir, profileID)
		if err != nil {
			return false, err
		}
		if len(imported) > 0 {
			if _, err := fmt.Fprintf(stderr, "Shared credentials: %s\r\n", strings.Join(imported, ", ")); err != nil {
				return false, err
			}
		} else {
			if _, err := fmt.Fprintln(stderr, "No host credentials found to copy.\r"); err != nil {
				return false, err
			}
		}
	case 1:
		agents := []string{"claude", "codex", "opencode"}
		agentIdx, err := pickOption(in, stderr, "Select agent to log in", agents, "onboarding cancelled", "write onboarding prompt")
		if err != nil {
			return false, err
		}
		loginOpts := session.AgentOptions{
			Agent:   agents[agentIdx],
			Image:   opts.Image,
			Profile: profileID,
			DataDir: cfg.DataDir,
		}
		if err := session.Authenticate(cmd.Context(), loginOpts, "login", in, cmd.OutOrStdout(), stderr); err != nil {
			return false, err
		}
	}

	if err := profile.MarkOnboarded(cfg.DataDir); err != nil {
		return false, err
	}
	return startWorkspace, nil
}
