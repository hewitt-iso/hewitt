package cmd

import (
	"github.com/hewitt-iso/hewitt/gateway"
	"github.com/spf13/cobra"
)

// newGatewayCommand runs the session gateway inside its container. Hewitt
// starts it; it is hidden because users never run it themselves.
func newGatewayCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "gateway <config.json>",
		Short:  "Run a session network gateway (started by Hewitt inside its container)",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := gateway.LoadConfig(args[0])
			if err != nil {
				return err
			}
			s, err := gateway.New(cfg)
			if err != nil {
				return err
			}
			return s.ListenAndServe()
		},
	}
	return cmd
}
