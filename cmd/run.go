package cmd

import (
	"fmt"

	"github.com/hewitt-iso/hewitt/config"
	"github.com/hewitt-iso/hewitt/session"
	"github.com/spf13/cobra"
)

func newAgentCommand(action string, cfg *config.Config) *cobra.Command {
	var image, name, profile, repository, base string
	var file, mode, target, workspace, isolation string
	var danger []string
	var detach bool
	short := "Start an agent container with a persistent native profile"
	exampleAction := action
	switch action {
	case "login":
		short = "Log in to a subscription using the native agent in a container"
	case "status":
		short = "Show native authentication status for a profile"
		exampleAction = "auth status"
	case "logout":
		short = "Log out a shared profile using the native agent"
		exampleAction = "auth logout"
	}
	cmd := &cobra.Command{
		Use: action + " <agent>", Short: short,
		Long:      short + ".\n\nSupported adapters: claude, codex. Planned agents: opencode, gemini, copilot.\nRun images must contain tmux, env and the selected CLI on PATH and support the host UID/GID.\nA dedicated profile is mounted; no host credentials are imported.\nLogin requires an interactive terminal. Containers and profiles are preserved.\nContainers may share a profile; login/logout affects that profile. Run attaches by default; use --detach to start in the background.\nPress Ctrl-B then D to detach; hewitt attach reconnects to the same terminal.\nRun creates an isolated worktree and branch; mounts include shared Git metadata.",
		Example:   "  hewitt " + exampleAction + " codex --image hewitt/codex:local --profile personal",
		ValidArgs: []string{"claude", "codex", "opencode", "gemini", "copilot"},
		Args:      cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := session.AgentOptions{Image: image, Name: name, Profile: profile, DataDir: cfg.DataDir, Repository: repository}
			if len(args) > 0 {
				opts.Agent = args[0]
			}
			if action != "run" {
				return session.Authenticate(cmd.Context(), opts, action, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
			}
			// Unset flags leave their values to the Hewittfile and its defaults.
			if !cmd.Flags().Changed("profile") {
				opts.Profile = ""
			}
			if cmd.Flags().Changed("base") {
				opts.Base = base
			}
			opts.File, opts.Mode, opts.Target, opts.Workspace, opts.Isolation, opts.Danger = file, mode, target, workspace, isolation, danger
			id, err := session.RunAgent(cmd.Context(), opts, cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			if detach {
				_, err = fmt.Fprintln(cmd.OutOrStdout(), id)
				return err
			}
			if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "Session: %s\nDetach with Ctrl-B then D; reconnect with hewitt attach %s\n", id, id); err != nil {
				return fmt.Errorf("report session %q (container preserved): %w", id, err)
			}
			store, err := session.NewStore(cfg.DataDir)
			if err != nil {
				return err
			}
			return session.Attach(cmd.Context(), store, id, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "optional Docker container name")
	cmd.Flags().StringVar(&profile, "profile", "default", "persistent profile ID, scoped to the agent")
	if action != "run" {
		cmd.Flags().StringVar(&image, "image", "", "image containing the selected native CLI (required)")
		if err := cmd.MarkFlagRequired("image"); err != nil {
			panic(err)
		}
		return cmd
	}
	cmd.Args = cobra.MatchAll(cobra.MaximumNArgs(1), cobra.OnlyValidArgs)
	cmd.Use = "run [agent]"
	cmd.Example = "  hewitt run codex --image hewitt/codex:local --profile personal\n" +
		"  hewitt run                       # settings from ./Hewittfile\n" +
		"  hewitt run claude --mode exec --target py-agent\n" +
		"  hewitt run claude --isolation restricted --workspace copy"
	cmd.Long = short + ".\n\nFlags override the Hewittfile (Hewittfile in --repo, or --file); see README for its instructions.\nSupported adapters: claude, codex. Planned agents: opencode, gemini, copilot.\nA dedicated profile is mounted; no host credentials are imported."
	cmd.Flags().StringVar(&image, "image", "", "image containing the selected native CLI (or FROM)")
	cmd.Flags().StringVar(&repository, "repo", ".", "source directory or Git repository (default: current directory)")
	cmd.Flags().StringVar(&base, "base", "HEAD", "commit or ref to start the new worktree from")
	cmd.Flags().BoolVarP(&detach, "detach", "d", false, "start without attaching; print the session ID")
	cmd.Flags().StringVarP(&file, "file", "f", "", "Hewittfile path (default: Hewittfile in --repo)")
	cmd.Flags().StringVar(&mode, "mode", "", "launch mode: new, exec, or sidecar (default new)")
	cmd.Flags().StringVar(&target, "target", "", "existing container for exec and sidecar modes")
	cmd.Flags().StringVar(&workspace, "workspace", "", "workspace: worktree, direct, or copy (default worktree)")
	cmd.Flags().StringVar(&isolation, "isolation", "", "isolation level: shared, isolated, restricted, or hardened")
	cmd.Flags().StringSliceVar(&danger, "danger", nil, "explicitly allow weakening: network, host-paths, writable-root, symlinks")
	return cmd
}
