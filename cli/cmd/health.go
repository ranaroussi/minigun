package cmd

import (
	"github.com/spf13/cobra"
)

var healthCmd = &cobra.Command{
	Use:   "health",
	Short: "Check whether the MiniGun server is reachable and healthy",
	RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := newClient().Get("/healthz")
		if err != nil {
			return err
		}
		printJSON(resp.Body)
		return resp.Error()
	},
}

func init() {
	rootCmd.AddCommand(healthCmd)
}
