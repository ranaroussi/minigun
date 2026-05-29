package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
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
	bulkTestMode   bool
	bulkSendAt     string

	singleTo           string
	singleSubject      string
	singlePreheader    string
	singleFrom         string
	singleReplyTo      string
	singleCompany      string
	singleList         string
	singleDomain       string
	singleMDFile       string
	singleHTMLFile     string
	singleTextFile     string
	singleTemplateFile string
	singleTestMode     bool
	singleSendAt       string

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
			"test_mode":    bulkTestMode,
		}
		if bulkDomain != "" {
			body["domain"] = bulkDomain
		}
		if bulkSendAt != "" {
			body["send_at"] = bulkSendAt
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
		var template string
		if singleTemplateFile != "" {
			b, e := os.ReadFile(singleTemplateFile)
			if e != nil {
				return fmt.Errorf("read --template: %w", e)
			}
			template = string(b)
		}
		body := map[string]any{
			"to":        singleTo,
			"subject":   singleSubject,
			"preheader": singlePreheader,
			"from":      singleFrom,
			"reply_to":  singleReplyTo,
			"company":   singleCompany,
			"list":      singleList,
			"md":        md,
			"html":      html,
			"text":      text,
			"template":  template,
			"test_mode": singleTestMode,
		}
		if singleDomain != "" {
			body["domain"] = singleDomain
		}
		if singleSendAt != "" {
			body["send_at"] = singleSendAt
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

var sendCancelCmd = &cobra.Command{
	Use:   "cancel <send_id>",
	Short: "Cancel a scheduled (or not-yet-started) send before it dispatches",
	Long: `Cancels a send that has not started yet — i.e. one still in the
'scheduled' (future-dated) or 'queued' state — by transitioning it to
'cancelled'. This is the unschedule path for sends created with --send-at.

A send that is already running, completed, failed, or cancelled cannot be
cancelled this way (returns 409).`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		resp, err := newClient().Post(fmt.Sprintf("/send/%s/cancel", args[0]), nil)
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

var (
	recipientsLimit  int
	recipientsCursor string
	recipientsAll    bool

	clicksLimit  int
	clicksCursor string
	clicksAll    bool
)

var sendRecipientsCmd = &cobra.Command{
	Use:   "recipients <send_id>",
	Short: "List per-recipient message engagement for a send (sent/delivered/opens/clicks/failure)",
	Long: `Returns one row per recipient of a send, summarizing how that contact
interacted with the message: sent/delivered timestamps, first/last open
and click with counts, and failure/complaint/unsubscribe state.

This is the per-message detail tier (contact_message_engagement). For a
contact's lifetime engagement across a whole list, use
'minigun contact engagement'.

Pagination is a keyset cursor over contact_id. Pass --cursor with the
value from next_cursor, or --all to follow pagination automatically.

Requires EVENTS_ARCHIVE_ENABLED on the server side.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		sendID := args[0]
		cli := newClient()
		cursor := recipientsCursor
		first := true
		for {
			q := url.Values{}
			if recipientsLimit > 0 {
				q.Set("limit", fmt.Sprintf("%d", recipientsLimit))
			}
			if cursor != "" {
				q.Set("cursor", cursor)
			}
			path := fmt.Sprintf("/send/%s/recipients", sendID)
			if enc := q.Encode(); enc != "" {
				path += "?" + enc
			}
			resp, err := cli.Get(path)
			if err != nil {
				return err
			}
			if !resp.OK() {
				printJSON(resp.Body)
				return resp.Error()
			}
			if !recipientsAll {
				printJSON(resp.Body)
				return nil
			}
			var page struct {
				Items      []json.RawMessage `json:"items"`
				NextCursor string            `json:"next_cursor"`
			}
			if err := json.Unmarshal(resp.Body, &page); err != nil {
				return err
			}
			if first {
				fmt.Println("[")
				first = false
			}
			for i, item := range page.Items {
				if i > 0 {
					fmt.Println(",")
				}
				fmt.Print("  ", string(item))
			}
			if page.NextCursor == "" {
				if len(page.Items) > 0 {
					fmt.Println()
				}
				fmt.Println("]")
				return nil
			}
			if len(page.Items) > 0 {
				fmt.Println(",")
			}
			cursor = page.NextCursor
		}
	},
}

var sendClicksCmd = &cobra.Command{
	Use:   "clicks <send_id>",
	Short: "List per-URL clicks for a send (one row per contact + clicked link)",
	Long: `Returns one row per (recipient, clicked URL) for a send: the canonical
destination URL, first/last click timestamps, and a click count.

This is the per-link detail behind contact_message_engagement.total_clicks
— useful for segmenting an audience by what they clicked. URLs are stored
canonical (scheme+host lowercased, query string and fragment stripped).

Pagination is a keyset cursor over (contact_id, url). Pass --cursor with
the value from next_cursor, or --all to follow pagination automatically.

Requires EVENTS_ARCHIVE_ENABLED on the server side.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		sendID := args[0]
		cli := newClient()
		cursor := clicksCursor
		first := true
		for {
			q := url.Values{}
			if clicksLimit > 0 {
				q.Set("limit", fmt.Sprintf("%d", clicksLimit))
			}
			if cursor != "" {
				q.Set("cursor", cursor)
			}
			path := fmt.Sprintf("/send/%s/clicks", sendID)
			if enc := q.Encode(); enc != "" {
				path += "?" + enc
			}
			resp, err := cli.Get(path)
			if err != nil {
				return err
			}
			if !resp.OK() {
				printJSON(resp.Body)
				return resp.Error()
			}
			if !clicksAll {
				printJSON(resp.Body)
				return nil
			}
			var page struct {
				Items      []json.RawMessage `json:"items"`
				NextCursor string            `json:"next_cursor"`
			}
			if err := json.Unmarshal(resp.Body, &page); err != nil {
				return err
			}
			if first {
				fmt.Println("[")
				first = false
			}
			for i, item := range page.Items {
				if i > 0 {
					fmt.Println(",")
				}
				fmt.Print("  ", string(item))
			}
			if page.NextCursor == "" {
				if len(page.Items) > 0 {
					fmt.Println()
				}
				fmt.Println("]")
				return nil
			}
			if len(page.Items) > 0 {
				fmt.Println(",")
			}
			cursor = page.NextCursor
		}
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
	sendBulkCmd.Flags().BoolVar(&bulkTestMode, "testmode", false, "Mailgun test mode: messages are accepted and logged but not delivered. Useful for dry runs.")
	sendBulkCmd.Flags().StringVar(&bulkSendAt, "send-at", "", "Schedule the send for a future RFC3339 time (e.g. 2026-06-01T09:00:00Z). Omit to send now. Cancel with 'send cancel'.")
	_ = sendBulkCmd.MarkFlagRequired("list")
	_ = sendBulkCmd.MarkFlagRequired("subject")
	_ = sendBulkCmd.MarkFlagRequired("from")

	sendSingleCmd.Flags().StringVar(&singleTo, "to", "", "Recipient email")
	sendSingleCmd.Flags().StringVar(&singleSubject, "subject", "", "Email subject")
	sendSingleCmd.Flags().StringVar(&singlePreheader, "preheader", "", "Preheader text (hidden inbox-preview snippet)")
	sendSingleCmd.Flags().StringVar(&singleFrom, "from", "", "From header")
	sendSingleCmd.Flags().StringVar(&singleReplyTo, "reply-to", "", "Reply-To address")
	sendSingleCmd.Flags().StringVar(&singleCompany, "company", "", "Company id or slug (resolves sending domain)")
	sendSingleCmd.Flags().StringVar(&singleList, "list", "", "Optional list id or slug. When set, MiniGun upserts the recipient's subscription, signs a per-recipient unsubscribe token, and adds List-Unsubscribe / List-Unsubscribe-Post headers (recommended for marketing / welcome / lifecycle mail). Omit for pure transactional mail (receipts, password resets) where there is no opt-out.")
	sendSingleCmd.Flags().StringVar(&singleDomain, "domain", "", "Override sending domain for this send (uses company.sending_domain if omitted; resolved value is persisted on the send row)")
	sendSingleCmd.Flags().StringVar(&singleMDFile, "md", "", "Markdown body file")
	sendSingleCmd.Flags().StringVar(&singleHTMLFile, "html", "", "HTML body file")
	sendSingleCmd.Flags().StringVar(&singleTextFile, "text", "", "Plain-text body file (optional)")
	sendSingleCmd.Flags().StringVar(&singleTemplateFile, "template", "", "HTML wrapper template file ({{content}} is replaced with the rendered body)")
	sendSingleCmd.Flags().BoolVar(&singleTestMode, "testmode", false, "Mailgun test mode: message is accepted and logged but not delivered. Useful for dry runs.")
	sendSingleCmd.Flags().StringVar(&singleSendAt, "send-at", "", "Schedule the send for a future RFC3339 time (e.g. 2026-06-01T09:00:00Z). Omit to send now. Cancel with 'send cancel'.")
	_ = sendSingleCmd.MarkFlagRequired("to")
	_ = sendSingleCmd.MarkFlagRequired("subject")
	_ = sendSingleCmd.MarkFlagRequired("from")
	_ = sendSingleCmd.MarkFlagRequired("company")

	sendResumeCmd.Flags().BoolVar(&resumeForce, "force", false, "Resume even when in-flight batches are present (may cause duplicate sends)")

	sendStatusCmd.Flags().BoolVarP(&statusWatch, "watch", "w", false, "Poll status until the send reaches a terminal state")
	sendStatusCmd.Flags().DurationVar(&statusInterval, "interval", 2*time.Second, "Polling interval when --watch is set")

	sendRecipientsCmd.Flags().IntVar(&recipientsLimit, "limit", 100, "Page size (default 100, max 500)")
	sendRecipientsCmd.Flags().StringVar(&recipientsCursor, "cursor", "", "Opaque pagination cursor from a previous page's next_cursor")
	sendRecipientsCmd.Flags().BoolVar(&recipientsAll, "all", false, "Follow next_cursor and emit all recipients as one JSON array")

	sendClicksCmd.Flags().IntVar(&clicksLimit, "limit", 100, "Page size (default 100, max 500)")
	sendClicksCmd.Flags().StringVar(&clicksCursor, "cursor", "", "Opaque pagination cursor from a previous page's next_cursor")
	sendClicksCmd.Flags().BoolVar(&clicksAll, "all", false, "Follow next_cursor and emit all click rows as one JSON array")

	sendCmd.AddCommand(sendBulkCmd, sendSingleCmd, sendResumeCmd, sendCancelCmd, sendStatusCmd, sendStatsCmd, sendRecipientsCmd, sendClicksCmd)
	rootCmd.AddCommand(sendCmd)
}
