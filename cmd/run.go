package cmd

import (
	"fmt"
	"slices"

	"github.com/hewitt-iso/hewitt/agent"
	"github.com/hewitt-iso/hewitt/config"
	"github.com/hewitt-iso/hewitt/profile"
	"github.com/hewitt-iso/hewitt/session"
	"github.com/spf13/cobra"
)

func newAgentCommand(action string, cfg *config.Config) *cobra.Command {
	var image, name, profileID, repository, base string
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
		Long:      short + ".\n\nAll agents use ghcr.io/hewitt-iso/hewitt:latest by default (hewitt.yml image applies to run only).\nSupported adapters: claude, codex, opencode, pi, antigravity. Planned agents: gemini, copilot.\nRun images must contain tmux, env and the selected CLI on PATH and support the host UID/GID.\nA dedicated profile is mounted; no host credentials are imported.\nLogin requires an interactive terminal. Containers and profiles are preserved.\nContainers may share a profile; login/logout affects that profile. Run attaches by default; use --detach to start in the background.\nPress Ctrl-D to detach; hewitt attach reconnects to the same terminal. Ctrl-C stops the agent.\nRun creates an isolated worktree and branch; mounts include shared Git metadata.",
		Example:   "  hewitt " + exampleAction + " codex --profile personal",
		ValidArgs: append(slices.Clone(agent.Names), "gemini", "copilot"),
		Args:      cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("image") && image == "" {
				return fmt.Errorf("image must be nonempty")
			}
			opts := session.AgentOptions{Image: image, Name: name, Profile: profileID, DataDir: cfg.DataDir, Repository: repository}
			if len(args) > 0 {
				opts.Agent = args[0]
			}
			if action != "run" {
				return session.Authenticate(cmd.Context(), opts, action, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
			}
			// Unset flags leave their values to hewitt.yml and its defaults.
			if !cmd.Flags().Changed("profile") {
				opts.Profile = ""
			}
			if cmd.Flags().Changed("base") {
				opts.Base = base
			}
			opts.File, opts.Mode, opts.Target, opts.Workspace, opts.Isolation, opts.Danger = file, mode, target, workspace, isolation, danger
			if isInteractive(cmd) && !profile.IsOnboarded(cfg.DataDir) {
				start, err := runOnboarding(cmd, cfg, &opts)
				if err != nil {
					return err
				}
				if !start {
					_, err := fmt.Fprintln(cmd.ErrOrStderr(), "Onboarding complete. Run 'hewitt run' to start a workspace.")
					return err
				}
			}
			return executeRun(cmd, cfg, opts, detach)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "optional Docker container name")
	cmd.Flags().StringVar(&profileID, "profile", "default", "persistent profile ID, scoped to the agent")
	if action != "run" {
		cmd.Flags().StringVar(&image, "image", "", "override the default Claude/Codex image (required for OpenCode)")
		return cmd
	}
	cmd.Args = cobra.MatchAll(cobra.MaximumNArgs(1), cobra.OnlyValidArgs)
	cmd.Use = "run [agent]"
	cmd.Example = "  hewitt run                       # interactive agent picker in workspace container\n" +
		"  hewitt run codex --profile personal\n" +
		"  hewitt run claude --mode exec --target py-agent\n" +
		"  hewitt run claude --isolation restricted --workspace copy"
	cmd.Long = short + ".\n\nFlags override hewitt.yml (hewitt.yml in --repo, or --file); see README for its keys.\nSelects ghcr.io/hewitt-iso/hewitt:latest automatically (hewitt.yml image applies to run only) and installs agents on demand into <data_dir>/tools.\nSupported adapters: claude, codex, opencode, pi, antigravity. Planned agents: gemini, copilot.\nA dedicated profile is mounted; no host credentials are imported."
	cmd.Flags().StringVar(&image, "image", "", "override the default image or hewitt.yml image")
	cmd.Flags().StringVar(&repository, "repo", ".", "source directory or Git repository (default: current directory)")
	cmd.Flags().StringVar(&base, "base", "HEAD", "commit or ref to start the new worktree from")
	cmd.Flags().BoolVarP(&detach, "detach", "d", false, "start without attaching; print the session ID")
	cmd.Flags().StringVarP(&file, "file", "f", "", "hewitt.yml path (default: hewitt.yml in --repo)")
	cmd.Flags().StringVar(&mode, "mode", "", "launch mode: new, exec, or sidecar (default new)")
	cmd.Flags().StringVar(&target, "target", "", "existing container for exec and sidecar modes")
	cmd.Flags().StringVar(&workspace, "workspace", "", "workspace: worktree, direct, or copy (default worktree)")
	cmd.Flags().StringVar(&isolation, "isolation", "", "isolation level: shared, isolated, restricted, or hardened")
	cmd.Flags().StringSliceVar(&danger, "danger", nil, "explicitly allow weakening: network, host-paths, writable-root, symlinks")
	return cmd
}

func executeRun(cmd *cobra.Command, cfg *config.Config, opts session.AgentOptions, detach bool) error {
	id, err := session.RunAgent(cmd.Context(), opts, cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	if detach {
		_, err = fmt.Fprintln(cmd.OutOrStdout(), id)
		return err
	}
	reconnect := shortID(id)
	if opts.Name != "" {
		reconnect = opts.Name
	}
	if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "Session: %s\nDetach with Ctrl-D, stop with Ctrl-C; reconnect with hewitt attach %s\n", id, reconnect); err != nil {
		return fmt.Errorf("report session %q (container preserved): %w", id, err)
	}
	store, err := session.NewStore(cfg.DataDir)
	if err != nil {
		return err
	}
	return session.Attach(cmd.Context(), store, id, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
}
