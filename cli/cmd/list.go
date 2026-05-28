package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	listName        string
	listSlug        string
	listCompany     string
	listDomain      string
	listDescription string
	listWeight      int

	pruneByCount      int64
	pruneByRecency    int64
	pruneNoDelivery   int64
	pruneApply        bool
	pruneLimit        int
	pruneSampleSize   int
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
		if listDomain != "" {
			body["domain"] = listDomain
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

var listPruneCmd = &cobra.Command{
	Use:   "prune <list>",
	Short: "Unsubscribe dormant contacts from a list (DRY-RUN by default; pass --apply to commit)",
	Long: `List-hygiene executor. Finds currently-subscribed contacts that match
one or more dormancy criteria and unsubscribes them, writing an
audit row to unsubscribe_events with the matched reason.

Criteria (at least ONE must be set; multiple act as OR):
  --by-count N         messages_since_last_engagement >= N
  --by-recency D       no open or click in the last D days
  --no-delivery-for D  no delivered events in the last D days (anchored
                       on subscribed_at to avoid pruning brand-new
                       contacts that haven't yet received anything)

Safety: dry-run is the DEFAULT. The command returns the candidate
preview (count + sample + per-reason breakdown) without changing any
rows until you pass --apply.

Examples:
  minigun list prune newsletter --by-count 10                 # preview
  minigun list prune newsletter --by-count 10 --apply         # commit
  minigun list prune newsletter --by-recency 180 --apply      # 6-month rule
  minigun list prune newsletter --no-delivery-for 90 --apply  # never-engaged cohort`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		body := map[string]any{
			"min_messages_since_engagement": pruneByCount,
			"dormant_for_days":              pruneByRecency,
			"no_delivery_for_days":          pruneNoDelivery,
			"dry_run":                       !pruneApply,
			"limit":                         pruneLimit,
			"sample_size":                   pruneSampleSize,
		}
		resp, err := newClient().Post(fmt.Sprintf("/lists/%s/prune", args[0]), body)
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
	listCreateCmd.Flags().StringVar(&listDomain, "domain", "", "Mailgun sending domain (optional; inherits from the company if omitted)")
	listCreateCmd.Flags().StringVar(&listDescription, "description", "", "Description shown on the preferences page")
	listCreateCmd.Flags().IntVar(&listWeight, "weight", 0, "Display weight on the preferences page (default 10, lower = higher up)")
	_ = listCreateCmd.MarkFlagRequired("name")
	_ = listCreateCmd.MarkFlagRequired("slug")
	_ = listCreateCmd.MarkFlagRequired("company")

	listPruneCmd.Flags().Int64Var(&pruneByCount, "by-count", 0, "Match contacts with messages_since_last_engagement >= N (0 disables)")
	listPruneCmd.Flags().Int64Var(&pruneByRecency, "by-recency", 0, "Match contacts whose last open/click is older than D days (0 disables)")
	listPruneCmd.Flags().Int64Var(&pruneNoDelivery, "no-delivery-for", 0, "Match contacts with no delivered events in the last D days, anchored on subscribed_at (0 disables)")
	listPruneCmd.Flags().BoolVar(&pruneApply, "apply", false, "Commit the unsubscribes. WITHOUT this flag the command is dry-run.")
	listPruneCmd.Flags().IntVar(&pruneLimit, "limit", 1000, "Max candidates per call (default 1000, max 10000)")
	listPruneCmd.Flags().IntVar(&pruneSampleSize, "sample-size", 25, "Number of sample rows to include in the response")

	listCmd.AddCommand(listCreateCmd)
	listCmd.AddCommand(listListCmd)
	listCmd.AddCommand(listPruneCmd)
	rootCmd.AddCommand(listCmd)
}
