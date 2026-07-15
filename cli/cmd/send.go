package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ranaroussi/minigun/cli/internal/client"
	"github.com/ranaroussi/minigun/cli/internal/frontmatter"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// firstNonEmpty returns explicit if it has non-whitespace content, else fallback.
func firstNonEmpty(explicit, fallback string) string {
	if strings.TrimSpace(explicit) != "" {
		return explicit
	}
	return fallback
}

// requireSubjectFrom validates the two fields that may come from either a flag
// or Markdown frontmatter, after the merge. Reported together so the operator
// sees every missing value at once.
func requireSubjectFrom(subject, from string) error {
	var missing []string
	if strings.TrimSpace(subject) == "" {
		missing = append(missing, "subject")
	}
	if strings.TrimSpace(from) == "" {
		missing = append(missing, "from")
	}
	if len(missing) > 0 {
		return fmt.Errorf("%s required: pass the flag(s) or set them in the markdown frontmatter", strings.Join(missing, " and "))
	}
	return nil
}

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
	bulkNoProgress bool

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

	progressInterval time.Duration
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
		var template string
		if bulkTemplate != "" {
			b, e := os.ReadFile(bulkTemplate)
			if e != nil {
				return fmt.Errorf("read --template: %w", e)
			}
			template = string(b)
		}
		// Markdown frontmatter fills in subject/preheader/from/reply_to when
		// the flag wasn't passed; the block is stripped from the body.
		md, fm := frontmatter.Parse(md)
		subject := firstNonEmpty(bulkSubject, fm.Subject)
		from := firstNonEmpty(bulkFrom, fm.From)
		if err := requireSubjectFrom(subject, from); err != nil {
			return err
		}
		body := map[string]any{
			"list":         bulkList,
			"subject":      subject,
			"preheader":    firstNonEmpty(bulkPreheader, fm.Preheader),
			"from":         from,
			"reply_to":     firstNonEmpty(bulkReplyTo, fm.ReplyTo),
			"md":           md,
			"html":         html,
			"text":         text,
			"template":     template,
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
		c := newClient()
		resp, err := c.Post("/send/bulk", body)
		if err != nil {
			return err
		}
		printJSON(resp.Body)
		if err := resp.Error(); err != nil {
			return err
		}
		// After an immediate send (no --send-at), drop into the live progress
		// view so the operator can watch it drain. Skipped for scheduled sends
		// (nothing to watch yet), when opted out, or when stdout isn't a TTY
		// (piped / CI) so we never spew ANSI escapes into a log.
		if bulkSendAt != "" || bulkNoProgress || !term.IsTerminal(int(os.Stdout.Fd())) {
			return nil
		}
		var created struct {
			SendID string `json:"send_id"`
			ID     string `json:"id"`
		}
		if e := json.Unmarshal(resp.Body, &created); e != nil {
			return nil
		}
		id := firstNonEmpty(created.SendID, created.ID)
		if id == "" {
			return nil
		}
		return runProgress(c, id, time.Second)
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
		// Markdown frontmatter fills in subject/preheader/from/reply_to when
		// the flag wasn't passed; the block is stripped from the body.
		md, fm := frontmatter.Parse(md)
		subject := firstNonEmpty(singleSubject, fm.Subject)
		from := firstNonEmpty(singleFrom, fm.From)
		if err := requireSubjectFrom(subject, from); err != nil {
			return err
		}
		body := map[string]any{
			"to":        singleTo,
			"subject":   subject,
			"preheader": firstNonEmpty(singlePreheader, fm.Preheader),
			"from":      from,
			"reply_to":  firstNonEmpty(singleReplyTo, fm.ReplyTo),
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

type progressSnapshot struct {
	ID        string  `json:"id"`
	Subject   string  `json:"subject"`
	ListID    string  `json:"list_id"`
	ListSlug  string  `json:"list_slug"`
	Status    string  `json:"status"`
	CreatedAt string  `json:"created_at"`
	UpdatedAt string  `json:"updated_at"`
	LastError *string `json:"last_error"`
	Progress  struct {
		Sent             int `json:"sent"`
		CompletedBatches int `json:"completed_batches"`
		TotalBatches     int `json:"total_batches"`
	} `json:"progress"`
}

// fmtTime renders an RFC3339 timestamp in local time; falls back to the raw
// value (or an em dash when empty) so a malformed value never breaks the view.
func fmtTime(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Local().Format("2006-01-02 15:04:05 MST")
	}
	return s
}

func renderProgress(s progressSnapshot) string {
	list := "—"
	if s.ListSlug != "" || s.ListID != "" {
		switch {
		case s.ListSlug != "" && s.ListID != "":
			list = fmt.Sprintf("%s (%s)", s.ListSlug, s.ListID)
		case s.ListSlug != "":
			list = s.ListSlug
		default:
			list = s.ListID
		}
	}
	lastErr := "—"
	if s.LastError != nil && strings.TrimSpace(*s.LastError) != "" {
		lastErr = *s.LastError
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Progress report:\n\n")
	fmt.Fprintf(&b, "  Id:          %s\n", s.ID)
	fmt.Fprintf(&b, "  Subject:     %s\n", firstNonEmpty(s.Subject, "—"))
	fmt.Fprintf(&b, "  List:        %s\n\n", list)
	fmt.Fprintf(&b, "  Created at:  %s\n", fmtTime(s.CreatedAt))
	fmt.Fprintf(&b, "  Last update: %s\n", fmtTime(s.UpdatedAt))
	fmt.Fprintf(&b, "  Status:      %s\n\n", s.Status)
	fmt.Fprintf(&b, "  Progress:\n")
	fmt.Fprintf(&b, "    Sent: %d\n", s.Progress.Sent)
	fmt.Fprintf(&b, "    Batch: %d/%d\n\n", s.Progress.CompletedBatches, s.Progress.TotalBatches)
	fmt.Fprintf(&b, "  Last Error:  %s\n", lastErr)
	return b.String()
}

// runProgress drives the live, full-screen progress view for a send: it polls
// GET /send/:id every interval, clearing and redrawing the report, until the
// send reaches a terminal state or the user hits Ctrl+C. Shared by the
// explicit `send progress` command and the auto-launch after `send bulk`.
func runProgress(c *client.Client, id string, interval time.Duration) error {
	if interval <= 0 {
		interval = time.Second
	}
	path := fmt.Sprintf("/send/%s", id)
	// Restore the cursor on Ctrl+C so the terminal isn't left hidden.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)
	go func() {
		<-sig
		fmt.Print("\033[?25h\n")
		os.Exit(0)
	}()
	fmt.Print("\033[?25l")       // hide cursor while live-updating
	defer fmt.Print("\033[?25h") // show it again on normal exit
	for {
		resp, err := c.Get(path)
		if err != nil {
			return err
		}
		if err := resp.Error(); err != nil {
			return err
		}
		var snap progressSnapshot
		if err := json.Unmarshal(resp.Body, &snap); err != nil {
			printJSON(resp.Body)
			return err
		}
		fmt.Print("\033[H\033[2J") // cursor home + clear screen
		fmt.Print(renderProgress(snap))
		if terminal(snap.Status) {
			return nil
		}
		time.Sleep(interval)
	}
}

func newProgressCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "progress <send_id>",
		Short: "Live, full-screen progress report for a send (refreshes every second until completed or Ctrl+C)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runProgress(newClient(), args[0], progressInterval)
		},
	}
	c.Flags().DurationVar(&progressInterval, "interval", time.Second, "Refresh interval for the live progress report")
	return c
}

// sendProgressCmd is the canonical `minigun send progress`; progressTopCmd is a
// top-level `minigun progress` alias so both invocations work.
var sendProgressCmd = newProgressCmd()
var progressTopCmd = newProgressCmd()

var statsForce bool

var sendStatsCmd = &cobra.Command{
	Use:   "stats <send_id>",
	Short: "Show send aggregate stats (delivered, opened, etc.)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := fmt.Sprintf("/send/%s/stats", args[0])
		if statsForce {
			path += "?force=1"
		}
		resp, err := newClient().Get(path)
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

Requires ENGAGEMENT_STATS_ENABLED on the server side.`,
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

Requires ENGAGEMENT_STATS_ENABLED on the server side.`,
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
	sendBulkCmd.Flags().StringVar(&bulkTemplate, "template", "", "HTML wrapper template file ({{content}} is replaced with the rendered body)")
	sendBulkCmd.Flags().IntVar(&bulkBatchSize, "batch-size", 100, "Mailgun batch size")
	sendBulkCmd.Flags().IntVar(&bulkThrottleMS, "throttle-ms", 1000, "Sleep between batches in ms")
	sendBulkCmd.Flags().StringVar(&bulkNotifyTo, "notify", "", "Email to notify on completion or failure")
	sendBulkCmd.Flags().StringVar(&bulkUnsubMode, "unsub-mode", "local", "Unsubscribe mode: local | redirect | external")
	sendBulkCmd.Flags().StringVar(&bulkUnsubRedir, "unsub-redir", "", "Redirect URL (for unsub-mode=redirect)")
	sendBulkCmd.Flags().StringVar(&bulkUnsubURL, "unsub-url", "", "External handler URL (for unsub-mode=external)")
	sendBulkCmd.Flags().BoolVar(&bulkTestMode, "testmode", false, "Mailgun test mode: messages are accepted and logged but not delivered. Useful for dry runs.")
	sendBulkCmd.Flags().StringVar(&bulkSendAt, "send-at", "", "Schedule the send for a future RFC3339 time (e.g. 2026-06-01T09:00:00Z). Omit to send now. Cancel with 'send cancel'.")
	sendBulkCmd.Flags().BoolVar(&bulkNoProgress, "no-progress", false, "Don't auto-open the live progress view after an immediate send (default: open it when stdout is a terminal)")
	_ = sendBulkCmd.MarkFlagRequired("list")
	// subject/from are not marked required: they may be supplied via Markdown
	// frontmatter instead. The RunE validates after the frontmatter merge.

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
	_ = sendSingleCmd.MarkFlagRequired("company")
	// subject/from are not marked required: they may be supplied via Markdown
	// frontmatter instead. The RunE validates after the frontmatter merge.

	sendResumeCmd.Flags().BoolVar(&resumeForce, "force", false, "Resume even when in-flight batches are present (may cause duplicate sends)")

	sendStatsCmd.Flags().BoolVar(&statsForce, "force", false, "Bypass the cache and fetch the latest numbers from Mailgun now (also refreshes the stored snapshot)")

	sendStatusCmd.Flags().BoolVarP(&statusWatch, "watch", "w", false, "Poll status until the send reaches a terminal state")
	sendStatusCmd.Flags().DurationVar(&statusInterval, "interval", 2*time.Second, "Polling interval when --watch is set")

	sendRecipientsCmd.Flags().IntVar(&recipientsLimit, "limit", 100, "Page size (default 100, max 500)")
	sendRecipientsCmd.Flags().StringVar(&recipientsCursor, "cursor", "", "Opaque pagination cursor from a previous page's next_cursor")
	sendRecipientsCmd.Flags().BoolVar(&recipientsAll, "all", false, "Follow next_cursor and emit all recipients as one JSON array")

	sendClicksCmd.Flags().IntVar(&clicksLimit, "limit", 100, "Page size (default 100, max 500)")
	sendClicksCmd.Flags().StringVar(&clicksCursor, "cursor", "", "Opaque pagination cursor from a previous page's next_cursor")
	sendClicksCmd.Flags().BoolVar(&clicksAll, "all", false, "Follow next_cursor and emit all click rows as one JSON array")

	sendCmd.AddCommand(sendBulkCmd, sendSingleCmd, sendResumeCmd, sendCancelCmd, sendStatusCmd, sendProgressCmd, sendStatsCmd, sendRecipientsCmd, sendClicksCmd)
	rootCmd.AddCommand(sendCmd)
	rootCmd.AddCommand(progressTopCmd)
}
