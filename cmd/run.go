package cmd

import (
	"fmt"
	"os"
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
	var ssh bool
	var detach, separate bool
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
		Long:      short + ".\n\nAll agents use ghcr.io/hewitt-iso/hewitt:latest by default (hewitt.yml image applies to run only).\nSupported adapters: claude, codex, opencode, pi, antigravity. Planned agents: gemini, copilot.\nRun images must contain tmux, env and the selected CLI on PATH and support the host UID/GID.\nA dedicated profile is mounted; no host credentials are imported.\nLogin requires an interactive terminal. Containers and profiles are preserved.\nContainers may share a profile; login/logout affects that profile. Run attaches by default; use --detach to start in the background.\nPress Ctrl-B, D to detach; hewitt attach reconnects to the same terminal. Ctrl-C and Ctrl-D are handled by the agent.\nRun keeps the session repository in a Docker volume; the host repository is only read.\nhewitt sync imports the session branches as hewitt/<name>/<branch>.",
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
			opts.Separate, opts.Shell = separate, cmd.Name() == "shell"
			opts.File, opts.Mode, opts.Target, opts.Workspace, opts.Isolation, opts.Danger, opts.SSH = file, mode, target, workspace, isolation, danger, ssh
			if isInteractive(cmd) && !profile.IsOnboarded(cfg.DataDir) {
				start, err := runOnboarding(cmd, cfg, &opts)
				if err != nil {
					return err
				}
				if !start {
					_, err := fmt.Fprintf(cmd.ErrOrStderr(), "Onboarding complete. Run %shewitt run%s to start a workspace.\n",
						ansiBoldCyan, ansiReset)
					return err
				}
			}
			return executeRun(cmd, cfg, opts, detach)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "workspace name (different names select separate containers)")
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
	cmd.Long = short + ".\n\nFlags override hewitt.yml (hewitt.yml in --repo, or --file); see README for its keys.\nSelects ghcr.io/hewitt-iso/hewitt:latest automatically (hewitt.yml image applies to run only) and installs agents on demand into <data_dir>/tools.\nSupported adapters: claude, codex, opencode, pi, antigravity. Planned agents: gemini, copilot.\nA dedicated profile is mounted; no host credentials are imported.\nDefault volume runs reuse a workspace container across agents with matching settings. Each run opens a fresh foreground process; use host tmux to keep it connected. --separate creates another container. hewitt shell opens a shell. Legacy worktree/copy/direct and exec/sidecar modes retain container tmux."
	cmd.Flags().StringVar(&image, "image", "", "override the default image or hewitt.yml image")
	cmd.Flags().StringVar(&repository, "repo", ".", "source directory or Git repository (default: current directory)")
	cmd.Flags().StringVar(&base, "base", "HEAD", "commit or ref to start the session branch from")
	cmd.Flags().BoolVarP(&detach, "detach", "d", false, "prepare a workspace without opening an agent; print its ID (legacy modes start detached)")
	cmd.Flags().BoolVar(&separate, "separate", false, "create a separate workspace container instead of reusing one")
	cmd.Flags().StringVarP(&file, "file", "f", "", "hewitt.yml path (default: hewitt.yml in --repo)")
	cmd.Flags().StringVar(&mode, "mode", "", "launch mode: new, exec, or sidecar (default new)")
	cmd.Flags().StringVar(&target, "target", "", "existing container for exec and sidecar modes")
	cmd.Flags().StringVar(&workspace, "workspace", "", "workspace: volume, worktree, direct, or copy (default volume)")
	cmd.Flags().StringVar(&isolation, "isolation", "", "isolation level: shared, isolated, restricted, or hardened")
	cmd.Flags().BoolVar(&ssh, "ssh", false, "forward the host ssh-agent so the agent can push over SSH")
	cmd.Flags().StringSliceVar(&danger, "danger", nil, "explicitly allow weakening: network, host-paths, writable-root, symlinks")
	return cmd
}

func executeRun(cmd *cobra.Command, cfg *config.Config, opts session.AgentOptions, detach bool) error {
	if profile.ShouldSyncConfigs(cfg.DataDir) {
		if homeDir, err := os.UserHomeDir(); err == nil {
			prof := opts.Profile
			if prof == "" {
				prof = "default"
			}
			_, _ = profile.ImportHostConfigs(cfg.DataDir, homeDir, prof)
		}
	}
	id, err := session.RunAgent(cmd.Context(), opts, cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	if detach {
		_, err = fmt.Fprintln(cmd.OutOrStdout(), id)
		return err
	}
	store, err := session.NewStore(cfg.DataDir)
	if err != nil {
		return err
	}
	m, err := store.Get(id)
	if err != nil {
		return err
	}
	if m.Terminal == "host" {
		if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "Workspace: %s (%s)\nUse your host terminal or tmux for navigation. Exit closes this process; the workspace stays running.\n", m.Name, id); err != nil {
			return err
		}
		return session.OpenWorkspace(cmd.Context(), store, id, opts, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
	}
	reconnect := shortID(id)
	if opts.Name != "" {
		reconnect = opts.Name
	}
	if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "Session: %s\nDetach with Ctrl-B, D; reconnect with hewitt attach %s\n", id, reconnect); err != nil {
		return fmt.Errorf("report session %q (container preserved): %w", id, err)
	}
	return session.Attach(cmd.Context(), store, id, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
}

func newShellCommand(cfg *config.Config) *cobra.Command {
	cmd := newAgentCommand("run", cfg)
	cmd.Use = "shell"
	cmd.Short = "Open a shell in the shared workspace container"
	cmd.Long = "Open a fresh shell in the workspace container. The container is reused across agent runs. Use exit or Ctrl-D to close the shell; use host tmux to detach."
	cmd.Example = "  hewitt shell\n  hewitt shell --repo /path/to/project"
	cmd.Args = cobra.NoArgs
	cmd.ValidArgs = nil
	return cmd
}
