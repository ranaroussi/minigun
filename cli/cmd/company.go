package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	companyName   string
	companySlug   string
	companyDomain string
)

var companyCmd = &cobra.Command{
	Use:   "company",
	Short: "Manage companies (group of lists shown together on the /manage page)",
}

var companyCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a company",
	RunE: func(cmd *cobra.Command, args []string) error {
		body := map[string]string{
			"name":   companyName,
			"slug":   companySlug,
			"domain": companyDomain,
		}
		resp, err := newClient().Post("/companies", body)
		if err != nil {
			return err
		}
		printJSON(resp.Body)
		return resp.Error()
	},
}

var companyListCmd = &cobra.Command{
	Use:   "ls",
	Short: "List all companies",
	RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := newClient().Get("/companies")
		if err != nil {
			return err
		}
		printJSON(resp.Body)
		return resp.Error()
	},
}

var companyListsCmd = &cobra.Command{
	Use:   "lists <company>",
	Short: "List the mailing lists that belong to a company",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := newClient().Get(fmt.Sprintf("/companies/%s/lists", args[0]))
		if err != nil {
			return err
		}
		printJSON(resp.Body)
		return resp.Error()
	},
}

func init() {
	companyCreateCmd.Flags().StringVar(&companyName, "name", "", "Human-readable company name")
	companyCreateCmd.Flags().StringVar(&companySlug, "slug", "", "URL-safe slug (lowercase, hyphens)")
	companyCreateCmd.Flags().StringVar(&companyDomain, "domain", "", "Mailgun sending domain for this company (e.g. mail.acme.com)")
	_ = companyCreateCmd.MarkFlagRequired("name")
	_ = companyCreateCmd.MarkFlagRequired("slug")
	_ = companyCreateCmd.MarkFlagRequired("domain")

	companyCmd.AddCommand(companyCreateCmd)
	companyCmd.AddCommand(companyListCmd)
	companyCmd.AddCommand(companyListsCmd)
	rootCmd.AddCommand(companyCmd)
}
