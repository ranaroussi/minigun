package cmd

import (
	"encoding/json"
	"errors"

	"github.com/spf13/cobra"
)

var contactCmd = &cobra.Command{
	Use:   "contact",
	Short: "Manage contacts",
}

var contactAddCmd = &cobra.Command{
	Use:   "add <list> <email>",
	Short: "Add or update a contact in a list",
	Args:  cobra.ExactArgs(2),
	RunE:  runContactAdd,
}

var contactParamsJSON string

func init() {
	contactAddCmd.Flags().StringVar(&contactParamsJSON, "params", "{}", "JSON object of contact parameters")
	contactCmd.AddCommand(contactAddCmd)
	rootCmd.AddCommand(contactCmd)
}

func runContactAdd(cmd *cobra.Command, args []string) error {
	list := args[0]
	email := args[1]
	var params map[string]any
	if contactParamsJSON != "" {
		if err := json.Unmarshal([]byte(contactParamsJSON), &params); err != nil {
			return errors.New("--params must be valid JSON")
		}
	}
	body, err := json.Marshal(map[string]any{
		"email":  email,
		"params": params,
	})
	if err != nil {
		return err
	}
	return postJSON("/lists/"+list+"/contacts", body)
}
