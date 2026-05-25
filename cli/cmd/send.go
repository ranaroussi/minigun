package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
)

var (
	bulkList       string
	bulkSubject    string
	bulkPreheader  string
	bulkFrom       string
	bulkReplyTo    string
	bulkDomain     string
	bulkMDFile     string
	bulkHTMLFile   string
	bulkTextFile   string
	bulkTemplate   string
	bulkBatchSize  int
	bulkThrottleMS int
	bulkNotifyTo   string
	bulkUnsubMode  string
	bulkUnsubRedir string
	bulkUnsubURL   string

	singleTo       string
	singleSubject  string
	singleFrom     string
	singleReplyTo  string
	singleCompany  string
	singleDomain   string
	singleMDFile   string
	singleHTMLFile string
	singleTextFile string

	resumeForce bool

	statusWatch    bool
	statusInterval time.Duration
)

var sendCmd = &cobra.Command{
	Use:   "send",
	Short: "Send and inspect email sends",
}

var sendBulkCmd = &cobra.Command{
	Use:   "bulk",
	Short: "Trigger a bulk send to a list",
	RunE: func(cmd *cobra.Command, args []string) error {
		md, html, text, err := readBodies(bulkMDFile, bulkHTMLFile, bulkTextFile)
		if err != nil {
			return err
		}
		body := map[string]any{
			"list":         bulkList,
			"subject":      bulkSubject,
			"preheader":    bulkPreheader,
			"from":         bulkFrom,
			"reply_to":     bulkReplyTo,
			"md":           md,
			"html":         html,
			"text":         text,
			"template":     bulkTemplate,
			"batch_size":   bulkBatchSize,
			"throttle_ms":  bulkThrottleMS,
			"notify_email": bulkNotifyTo,
			"unsub_mode":   bulkUnsubMode,
			"unsub_redir":  bulkUnsubRedir,
			"unsub_url":    bulkUnsubURL,
		}
		if bulkDomain != "" {
			body["domain"] = bulkDomain
		}
		resp, err := newClient().Post("/send/bulk", body)
		if err != nil {
			return err
		}
		printJSON(resp.Body)
		return resp.Error()
	},
}

var sendSingleCmd = &cobra.Command{
	Use:   "single",
	Short: "Send a single transactional email",
	RunE: func(cmd *cobra.Command, args []string) error {
		md, html, text, err := readBodies(singleMDFile, singleHTMLFile, singleTextFile)
		if err != nil {
			return err
		}
		body := map[string]any{
			"to":       singleTo,
			"subject":  singleSubject,
			"from":     singleFrom,
			"reply_to": singleReplyTo,
			"company":  singleCompany,
			"md":       md,
			"html":     html,
			"text":     text,
		}
		if singleDomain != "" {
			body["domain"] = singleDomain
		}
		resp, err := newClient().Post("/send/single", body)
		if err != nil {
			return err
		}
		printJSON(resp.Body)
		return resp.Error()
	},
}

var sendResumeCmd = &cobra.Command{
	Use:   "resume <send_id>",
	Short: "Resume a paused or failed bulk send",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := fmt.Sprintf("/send/%s/resume", args[0])
		if resumeForce {
			path += "?force=1"
		}
		resp, err := newClient().Post(path, nil)
		if err != nil {
			return err
		}
		printJSON(resp.Body)
		return resp.Error()
	},
}

var sendStatusCmd = &cobra.Command{
	Use:   "status <send_id>",
	Short: "Show send status and progress",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		path := fmt.Sprintf("/send/%s", id)
		c := newClient()
		for {
			resp, err := c.Get(path)
			if err != nil {
				return err
			}
			if !statusWatch {
				printJSON(resp.Body)
				return resp.Error()
			}
			if err := resp.Error(); err != nil {
				return err
			}
			var snap struct {
				ID       string         `json:"id"`
				Status   string         `json:"status"`
				Progress map[string]any `json:"progress"`
			}
			if err := json.Unmarshal(resp.Body, &snap); err != nil {
				printJSON(resp.Body)
				return err
			}
			fmt.Fprintf(os.Stderr, "[%s] %s  sent=%v completed_batches=%v/%v remaining=%v\n",
				time.Now().Format("15:04:05"),
				snap.Status,
				snap.Progress["sent"],
				snap.Progress["completed_batches"],
				snap.Progress["total_batches"],
				snap.Progress["remaining"],
			)
			if terminal(snap.Status) {
				printJSON(resp.Body)
				return nil
			}
			time.Sleep(statusInterval)
		}
	},
}

var sendStatsCmd = &cobra.Command{
	Use:   "stats <send_id>",
	Short: "Show send aggregate stats (delivered, opened, etc.)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := newClient().Get(fmt.Sprintf("/send/%s/stats", args[0]))
		if err != nil {
			return err
		}
		printJSON(resp.Body)
		return resp.Error()
	},
}

func terminal(status string) bool {
	switch status {
	case "completed", "failed", "cancelled":
		return true
	}
	return false
}

func readBodies(mdFile, htmlFile, textFile string) (md, html, text string, err error) {
	if mdFile == "" && htmlFile == "" {
		return "", "", "", errors.New("either --md or --html is required")
	}
	if mdFile != "" {
		b, e := os.ReadFile(mdFile)
		if e != nil {
			return "", "", "", fmt.Errorf("read --md: %w", e)
		}
		md = string(b)
	}
	if htmlFile != "" {
		b, e := os.ReadFile(htmlFile)
		if e != nil {
			return "", "", "", fmt.Errorf("read --html: %w", e)
		}
		html = string(b)
	}
	if textFile != "" {
		b, e := os.ReadFile(textFile)
		if e != nil {
			return "", "", "", fmt.Errorf("read --text: %w", e)
		}
		text = string(b)
	}
	return md, html, text, nil
}

func init() {
	sendBulkCmd.Flags().StringVar(&bulkList, "list", "", "List slug or id")
	sendBulkCmd.Flags().StringVar(&bulkSubject, "subject", "", "Email subject")
	sendBulkCmd.Flags().StringVar(&bulkPreheader, "preheader", "", "Preheader text")
	sendBulkCmd.Flags().StringVar(&bulkFrom, "from", "", `From header, e.g. "Ran <ran@example.com>"`)
	sendBulkCmd.Flags().StringVar(&bulkReplyTo, "reply-to", "", "Reply-To address")
	sendBulkCmd.Flags().StringVar(&bulkDomain, "domain", "", "Override sending domain for this send (uses list.sending_domain if omitted; resolved value is persisted on the send row)")
	sendBulkCmd.Flags().StringVar(&bulkMDFile, "md", "", "Markdown body file")
	sendBulkCmd.Flags().StringVar(&bulkHTMLFile, "html", "", "HTML body file (used if --md is not provided)")
	sendBulkCmd.Flags().StringVar(&bulkTextFile, "text", "", "Plain-text body file (optional; auto-generated from --md/--html otherwise)")
	sendBulkCmd.Flags().StringVar(&bulkTemplate, "template", "", "Wrapper template name")
	sendBulkCmd.Flags().IntVar(&bulkBatchSize, "batch-size", 500, "Mailgun batch size")
	sendBulkCmd.Flags().IntVar(&bulkThrottleMS, "throttle-ms", 1000, "Sleep between batches in ms")
	sendBulkCmd.Flags().StringVar(&bulkNotifyTo, "notify", "", "Email to notify on completion or failure")
	sendBulkCmd.Flags().StringVar(&bulkUnsubMode, "unsub-mode", "local", "Unsubscribe mode: local | redirect | external")
	sendBulkCmd.Flags().StringVar(&bulkUnsubRedir, "unsub-redir", "", "Redirect URL (for unsub-mode=redirect)")
	sendBulkCmd.Flags().StringVar(&bulkUnsubURL, "unsub-url", "", "External handler URL (for unsub-mode=external)")
	_ = sendBulkCmd.MarkFlagRequired("list")
	_ = sendBulkCmd.MarkFlagRequired("subject")
	_ = sendBulkCmd.MarkFlagRequired("from")

	sendSingleCmd.Flags().StringVar(&singleTo, "to", "", "Recipient email")
	sendSingleCmd.Flags().StringVar(&singleSubject, "subject", "", "Email subject")
	sendSingleCmd.Flags().StringVar(&singleFrom, "from", "", "From header")
	sendSingleCmd.Flags().StringVar(&singleReplyTo, "reply-to", "", "Reply-To address")
	sendSingleCmd.Flags().StringVar(&singleCompany, "company", "", "Company id or slug (resolves sending domain)")
	sendSingleCmd.Flags().StringVar(&singleDomain, "domain", "", "Override sending domain for this send (uses company.sending_domain if omitted; resolved value is persisted on the send row)")
	sendSingleCmd.Flags().StringVar(&singleMDFile, "md", "", "Markdown body file")
	sendSingleCmd.Flags().StringVar(&singleHTMLFile, "html", "", "HTML body file")
	sendSingleCmd.Flags().StringVar(&singleTextFile, "text", "", "Plain-text body file (optional)")
	_ = sendSingleCmd.MarkFlagRequired("to")
	_ = sendSingleCmd.MarkFlagRequired("subject")
	_ = sendSingleCmd.MarkFlagRequired("from")
	_ = sendSingleCmd.MarkFlagRequired("company")

	sendResumeCmd.Flags().BoolVar(&resumeForce, "force", false, "Resume even when in-flight batches are present (may cause duplicate sends)")

	sendStatusCmd.Flags().BoolVarP(&statusWatch, "watch", "w", false, "Poll status until the send reaches a terminal state")
	sendStatusCmd.Flags().DurationVar(&statusInterval, "interval", 2*time.Second, "Polling interval when --watch is set")

	sendCmd.AddCommand(sendBulkCmd, sendSingleCmd, sendResumeCmd, sendStatusCmd, sendStatsCmd)
	rootCmd.AddCommand(sendCmd)
}
