package cmd

import (
	"github.com/spf13/cobra"
)

var (
	listName        string
	listSlug        string
	listCompany     string
	listDescription string
	listWeight      int
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "Manage lists",
}

var listCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a list",
	RunE: func(cmd *cobra.Command, args []string) error {
		body := map[string]any{
			"name":    listName,
			"slug":    listSlug,
			"company": listCompany,
		}
		if listDescription != "" {
			body["description"] = listDescription
		}
		if listWeight != 0 {
			body["weight"] = listWeight
		}
		resp, err := newClient().Post("/lists", body)
		if err != nil {
			return err
		}
		printJSON(resp.Body)
		return resp.Error()
	},
}

var listListCmd = &cobra.Command{
	Use:   "ls",
	Short: "List all lists",
	RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := newClient().Get("/lists")
		if err != nil {
			return err
		}
		printJSON(resp.Body)
		return resp.Error()
	},
}

func init() {
	listCreateCmd.Flags().StringVar(&listName, "name", "", "Human-readable list name")
	listCreateCmd.Flags().StringVar(&listSlug, "slug", "", "URL-safe slug (lowercase, hyphens)")
	listCreateCmd.Flags().StringVar(&listCompany, "company", "", "Company id or slug the list belongs to")
	listCreateCmd.Flags().StringVar(&listDescription, "description", "", "Description shown on the preferences page")
	listCreateCmd.Flags().IntVar(&listWeight, "weight", 0, "Display weight on the preferences page (default 10, lower = higher up)")
	_ = listCreateCmd.MarkFlagRequired("name")
	_ = listCreateCmd.MarkFlagRequired("slug")
	_ = listCreateCmd.MarkFlagRequired("company")

	listCmd.AddCommand(listCreateCmd)
	listCmd.AddCommand(listListCmd)
	rootCmd.AddCommand(listCmd)
}
