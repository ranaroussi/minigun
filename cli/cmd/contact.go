package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"

	"github.com/spf13/cobra"
)

var contactParamsJSON string

var contactCmd = &cobra.Command{
	Use:   "contact",
	Short: "Manage contacts and subscriptions",
}

var contactAddCmd = &cobra.Command{
	Use:   "add <list> <email>",
	Short: "Add or update a contact on a list",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		list := args[0]
		email := args[1]
		var params map[string]any
		if contactParamsJSON != "" {
			if err := json.Unmarshal([]byte(contactParamsJSON), &params); err != nil {
				return errors.New("--params must be valid JSON object")
			}
		}
		body := map[string]any{
			"email":  email,
			"params": params,
		}
		resp, err := newClient().Post(fmt.Sprintf("/lists/%s/contacts", list), body)
		if err != nil {
			return err
		}
		printJSON(resp.Body)
		return resp.Error()
	},
}

var contactUnsubscribeCmd = &cobra.Command{
	Use:   "unsubscribe <list> <email>",
	Short: "Unsubscribe a contact from a list (admin action)",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		list := args[0]
		email := args[1]
		body := map[string]any{"email": email}
		resp, err := newClient().Post(fmt.Sprintf("/lists/%s/unsubscribe", list), body)
		if err != nil {
			return err
		}
		printJSON(resp.Body)
		return resp.Error()
	},
}

var contactDeleteCmd = &cobra.Command{
	Use:   "delete <id-or-email>",
	Short: "Permanently delete a contact and all their subscriptions / unsubscribe events (use for hard-bounce cleanup)",
	Long: `Permanently removes a contact and every row that references them
(subscriptions across all lists + the unsubscribe-events audit log).

Use this for hard-bounce cleanup so the address cannot be picked up by
a future bulk send. For ordinary opt-outs, prefer 'contact unsubscribe'
so the suppression record survives.

Accepts either a contact id (c_XXXXXXXXXX) or a lowercase email.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := newClient().Delete(fmt.Sprintf("/contacts/%s", url.PathEscape(args[0])))
		if err != nil {
			return err
		}
		printJSON(resp.Body)
		return resp.Error()
	},
}

func init() {
	contactAddCmd.Flags().StringVar(&contactParamsJSON, "params", "", `JSON object of contact parameters, e.g. '{"first_name":"Ran"}'`)
	contactCmd.AddCommand(contactAddCmd, contactUnsubscribeCmd, contactDeleteCmd)
	rootCmd.AddCommand(contactCmd)
}
