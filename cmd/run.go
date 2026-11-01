package cmd

import (
	"fmt"

	"github.com/hewitt-iso/hewitt/config"
	"github.com/hewitt-iso/hewitt/docker"
	"github.com/hewitt-iso/hewitt/session"
	"github.com/spf13/cobra"
)

func newRunCommand(cfg *config.Config) *cobra.Command {
	var image, name string
	cmd := &cobra.Command{
		Use:       "run <agent>",
		Short:     "Create and start a container from an agent image",
		Long:      "Create and start a detached Docker container from an explicit image.\nThe image's default entrypoint and command must launch the selected agent.\n\nPlanned agents: claude (Claude Code), codex (Codex), opencode (OpenCode).\nHewitt does not yet install or authenticate agents, create worktrees, or provide\nterminal multiplexing. Use hewitt attach to connect to the container process. No host files or credentials are mounted.\n\nPrints the container ID on startup. The process may exit immediately if the\nimage's command finishes. Session metadata is saved under data_dir before startup.\nFailed starts preserve the container and its metadata for inspection. If saving fails, the container is\npreserved and startup is not attempted.",
		Example:   "  hewitt run codex --image my-codex:local\n  hewitt run claude --image my-claude:local --name hewitt-claude",
		ValidArgs: []string{"claude", "codex", "opencode"},
		Args:      cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := session.NewStore(cfg.DataDir)
			if err != nil {
				return err
			}
			metadata, err := session.Run(cmd.Context(), store, args[0], docker.CreateOptions{Image: image, Name: name})
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), metadata.ID)
			return err
		},
	}
	cmd.Flags().StringVar(&image, "image", "", "image whose default command starts the agent (required)")
	cmd.Flags().StringVar(&name, "name", "", "optional Docker container name")
	if err := cmd.MarkFlagRequired("image"); err != nil {
		panic(err) // The flag is defined immediately above; this is a programming error.
	}
	return cmd
}
