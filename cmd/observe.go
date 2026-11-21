package cmd

import (
	"github.com/hewitt-iso/hewitt/observer"
	"github.com/spf13/cobra"
)

// newObserveCommand runs a workspace's file observer inside its container.
// Hewitt starts it; it is hidden because users never run it themselves.
func newObserveCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "observe <config.json>",
		Short:  "Run a workspace file observer (started by Hewitt inside its container)",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := observer.LoadConfig(args[0])
			if err != nil {
				return err
			}
			return observer.Run(cfg)
		},
	}
}
