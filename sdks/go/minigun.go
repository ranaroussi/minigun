// Package minigun is the Go SDK for MiniGun
// (https://github.com/ranaroussi/minigun).
//
// Stdlib-only — no external imports — so it slots into any module
// without bloating its dependency graph. Targets Go 1.21+.
//
//	mg, err := minigun.New(os.Getenv("MINIGUN_API_URL"),
//	    minigun.WithToken(os.Getenv("MINIGUN_API_TOKEN")))
//	if err != nil { log.Fatal(err) }
//
//	if _, err := mg.AddContact(ctx, "newsletter", "alice@example.com",
//	    map[string]any{"first_name": "Alice"}); err != nil {
//	    log.Fatal(err)
//	}
//
//	res, err := mg.SendBulk(ctx, minigun.SendBulkArgs{
//	    List: "newsletter", Subject: "Hi", From: "Ran <r@x.com>",
//	    MD: "Hello {{first_name | 'there'}}!",
//	})
//
// Errors fall into two buckets:
//   - *TransportError — the request never completed (DNS, TLS, timeout, ctx)
//   - *APIError       — server returned non-2xx (Status + Body populated)
//
// Both implement `error` and are unwrappable; use errors.As to branch.
package minigun

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Unsub-mode constants for SendBulkArgs.UnsubMode.
const (
	UnsubLocal    = "local"
	UnsubRedirect = "redirect"
	UnsubExternal = "external"
)

// TransportError wraps a network / context error that prevented us
// from getting any HTTP response back.
type TransportError struct{ Err error }

func (e *TransportError) Error() string { return "minigun: transport error: " + e.Err.Error() }
func (e *TransportError) Unwrap() error { return e.Err }

// APIError is returned when the server replied with a 4xx or 5xx.
// Body is the raw decoded payload (typically map[string]any with an
// "error" key, but kept as `any` so callers can handle non-JSON
// responses without a second type-assertion).
type APIError struct {
	Status  int
	Body    any
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("minigun: API %d: %s", e.Status, e.Message) }

// Client is one connection-pool's worth of state. Safe for concurrent
// use — the underlying *http.Client is, and we never mutate fields
// after construction.
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
	userAgent  string
}

// Option configures a Client at construction time.
type Option func(*Client)

// WithToken sets the bearer token sent on every request. Required
// when the server has MINIGUN_API_TOKEN configured.
func WithToken(t string) Option { return func(c *Client) { c.token = t } }

// WithHTTPClient overrides the default *http.Client (10s connect /
// 120s total). Useful for injecting custom transports, proxies, or
// tracing.
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.httpClient = h } }

// WithTimeout sets the overall request timeout on the default
// http.Client. Ignored when WithHTTPClient is also passed (in which
// case you control the timeout on your injected client).
func WithTimeout(d time.Duration) Option {
	return func(c *Client) {
		if c.httpClient == nil {
			c.httpClient = &http.Client{Timeout: d}
		} else {
			c.httpClient.Timeout = d
		}
	}
}

// WithUserAgent overrides the default UA string. Helpful when you
// want server-side logs to attribute traffic to a specific service.
func WithUserAgent(ua string) Option { return func(c *Client) { c.userAgent = ua } }

// New constructs a Client. baseURL is the API origin (no trailing
// slash required); options are applied in order.
func New(baseURL string, opts ...Option) (*Client, error) {
	if baseURL == "" {
		return nil, errors.New("minigun: baseURL is required")
	}
	c := &Client{
		baseURL:   strings.TrimRight(baseURL, "/"),
		userAgent: "minigun-go/0.1",
	}
	for _, opt := range opts {
		opt(c)
	}
	if c.httpClient == nil {
		c.httpClient = &http.Client{Timeout: 120 * time.Second}
	}
	return c, nil
}

// ---------------------------------------------------------------------
// Contacts
// ---------------------------------------------------------------------

// AddContact upserts a contact + (re-)subscribes them to a list. Safe
// to call repeatedly with the same email: existing params are merged
// and any prior unsubscribe is cleared.
func (c *Client) AddContact(ctx context.Context, list, email string, params map[string]any) (map[string]any, error) {
	body := map[string]any{"email": email}
	if params != nil {
		body["params"] = params
	} else {
		body["params"] = nil
	}
	return c.post(ctx, "/lists/"+enc(list)+"/contacts", body)
}

// UnsubscribeContact marks a contact as unsubscribed on the given
// list. Preserves the row with subscribed=0 so future re-imports
// don't silently re-subscribe. For hard-bounce / spam-complaint
// cleanup, prefer DeleteContact.
func (c *Client) UnsubscribeContact(ctx context.Context, list, email string) (map[string]any, error) {
	return c.post(ctx, "/lists/"+enc(list)+"/unsubscribe", map[string]any{"email": email})
}

// DeleteContact permanently purges a contact + every row that
// references them (subscriptions on every list + unsubscribe-event
// audit log). Use for hard-bounce / scripted cleanup; the Mailgun
// webhook does this automatically for inbound bounce/complaint
// events. Accepts either the contact id (c_XXXX...) or the email.
func (c *Client) DeleteContact(ctx context.Context, idOrEmail string) (map[string]any, error) {
	return c.do(ctx, http.MethodDelete, "/contacts/"+enc(idOrEmail), nil)
}

// ListContacts returns a page of subscribers on a list. Pass back
// cursor from the previous response to walk forward; an empty cursor
// returns the first page.
func (c *Client) ListContacts(ctx context.Context, list, cursor string, limit int) (map[string]any, error) {
	if limit <= 0 {
		limit = 50
	}
	q := url.Values{}
	q.Set("limit", fmt.Sprintf("%d", limit))
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	return c.get(ctx, "/lists/"+enc(list)+"/contacts?"+q.Encode())
}

// ---------------------------------------------------------------------
// Sends
// ---------------------------------------------------------------------

// SendSingleArgs is the input to SendSingle. To/From/Subject/Company
// and one of MD/MDFile or HTML/HTMLFile are required. Company is the
// id or slug — the sending domain is resolved from it; pass Domain to
// override for this one send.
type SendSingleArgs struct {
	To           string
	From         string
	Subject      string
	Company      string
	MD           string
	MDFile       string
	HTML         string
	HTMLFile     string
	Text         string
	TextFile     string
	Template     string
	TemplateFile string
	Preheader    string
	ReplyTo      string
	Domain       string
	List         string
	TestMode     bool
}

// SendSingle sends a single transactional email. Returns immediately
// (202); the worker performs the Mailgun POST in the background —
// poll GetSend if you need the terminal status.
func (c *Client) SendSingle(ctx context.Context, args SendSingleArgs) (map[string]any, error) {
	md, err := resolveBody("MD", args.MD, args.MDFile)
	if err != nil {
		return nil, err
	}
	html, err := resolveBody("HTML", args.HTML, args.HTMLFile)
	if err != nil {
		return nil, err
	}
	text, err := resolveBody("Text", args.Text, args.TextFile)
	if err != nil {
		return nil, err
	}
	tpl, err := resolveBody("Template", args.Template, args.TemplateFile)
	if err != nil {
		return nil, err
	}
	if md == "" && html == "" {
		return nil, errors.New("minigun: either MD/MDFile or HTML/HTMLFile is required")
	}

	return c.post(ctx, "/send/single", map[string]any{
		"to":        args.To,
		"from":      args.From,
		"subject":   args.Subject,
		"preheader": args.Preheader,
		"company":   args.Company,
		"list":      args.List,
		"reply_to":  args.ReplyTo,
		"domain":    args.Domain,
		"md":        md,
		"html":      html,
		"text":      text,
		"template":  tpl,
		"test_mode": args.TestMode,
	})
}

// SendBulkArgs is the input to SendBulk. List/Subject/From and one of
// MD/MDFile or HTML/HTMLFile are required. BatchSize and ThrottleMs
// default to 500 and 1000 respectively when zero.
type SendBulkArgs struct {
	List         string
	Subject      string
	From         string
	MD           string
	MDFile       string
	HTML         string
	HTMLFile     string
	Text         string
	TextFile     string
	Template     string
	TemplateFile string
	ReplyTo      string
	Preheader    string
	Domain       string
	BatchSize    int
	ThrottleMs   int
	NotifyTo     string
	UnsubMode    string // UnsubLocal | UnsubRedirect | UnsubExternal; empty == local
	UnsubRedir   string
	UnsubURL     string
	TestMode     bool
}

// SendBulk triggers a bulk send. Returns 202 immediately with a
// send_id while the worker drives batches in the background. The
// first batch runs inline before the 202, so the response time
// scales with batch_size + Mailgun's latency, then subsequent
// batches self-chain on the server.
func (c *Client) SendBulk(ctx context.Context, args SendBulkArgs) (map[string]any, error) {
	md, err := resolveBody("MD", args.MD, args.MDFile)
	if err != nil {
		return nil, err
	}
	html, err := resolveBody("HTML", args.HTML, args.HTMLFile)
	if err != nil {
		return nil, err
	}
	text, err := resolveBody("Text", args.Text, args.TextFile)
	if err != nil {
		return nil, err
	}
	tpl, err := resolveBody("Template", args.Template, args.TemplateFile)
	if err != nil {
		return nil, err
	}
	if md == "" && html == "" {
		return nil, errors.New("minigun: either MD/MDFile or HTML/HTMLFile is required")
	}

	unsubMode := args.UnsubMode
	if unsubMode == "" {
		unsubMode = UnsubLocal
	}
	switch unsubMode {
	case UnsubLocal, UnsubRedirect, UnsubExternal:
	default:
		return nil, fmt.Errorf("minigun: UnsubMode must be %q, %q, or %q",
			UnsubLocal, UnsubRedirect, UnsubExternal)
	}
	if unsubMode == UnsubRedirect && args.UnsubRedir == "" {
		return nil, errors.New("minigun: UnsubRedir is required when UnsubMode=redirect")
	}
	if unsubMode == UnsubExternal && args.UnsubURL == "" {
		return nil, errors.New("minigun: UnsubURL is required when UnsubMode=external")
	}

	batchSize := args.BatchSize
	if batchSize == 0 {
		batchSize = 500
	}
	throttleMs := args.ThrottleMs
	if throttleMs == 0 {
		throttleMs = 1000
	}

	return c.post(ctx, "/send/bulk", map[string]any{
		"list":         args.List,
		"subject":      args.Subject,
		"from":         args.From,
		"reply_to":     args.ReplyTo,
		"preheader":    args.Preheader,
		"domain":       args.Domain,
		"md":           md,
		"html":         html,
		"text":         text,
		"template":     tpl,
		"batch_size":   batchSize,
		"throttle_ms":  throttleMs,
		"notify_email": args.NotifyTo,
		"unsub_mode":   unsubMode,
		"unsub_redir":  args.UnsubRedir,
		"unsub_url":    args.UnsubURL,
		"test_mode":    args.TestMode,
	})
}

// GetSend returns a one-shot snapshot of a send's status + per-batch
// progress.
func (c *Client) GetSend(ctx context.Context, sendID string) (map[string]any, error) {
	return c.get(ctx, "/send/"+enc(sendID))
}

// GetSendStats returns aggregate stats. DB-backed for completed
// sends; falls back to a live Mailgun Metrics API call for in-flight
// / just-finished ones.
func (c *Client) GetSendStats(ctx context.Context, sendID string) (map[string]any, error) {
	return c.get(ctx, "/send/"+enc(sendID)+"/stats")
}

// ResumeSend resumes a paused / failed send. Pass force=true ONLY
// if a batch was left in_flight — Mailgun may already have accepted
// it, so a retry can duplicate-send.
func (c *Client) ResumeSend(ctx context.Context, sendID string, force bool) (map[string]any, error) {
	path := "/send/" + enc(sendID) + "/resume"
	if force {
		path += "?force=1"
	}
	return c.post(ctx, path, map[string]any{})
}

// ListSendEventsArgs narrows what ListSendEvents returns.
// Zero values mean "no filter."
type ListSendEventsArgs struct {
	SendID  string
	Event   string // delivered | opened | clicked | failed | complained | unsubscribed
	SinceMs int64  // lower bound on event_timestamp_ms
	Limit   int    // page size (default 100, max 500)
	Cursor  string // opaque keyset cursor from a previous page's next_cursor
}

// ListSendEvents returns one page of archived Mailgun events for a
// send. Requires EVENTS_ARCHIVE_ENABLED on the server. The response
// shape is { items: [...], next_cursor?: string } — when next_cursor
// is absent, the page is the last one.
func (c *Client) ListSendEvents(ctx context.Context, a ListSendEventsArgs) (map[string]any, error) {
	if a.SendID == "" {
		return nil, errors.New("minigun: SendID is required")
	}
	q := url.Values{}
	if a.Event != "" {
		q.Set("event", a.Event)
	}
	if a.SinceMs > 0 {
		q.Set("since", fmt.Sprintf("%d", a.SinceMs))
	}
	if a.Limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", a.Limit))
	}
	if a.Cursor != "" {
		q.Set("cursor", a.Cursor)
	}
	path := "/send/" + enc(a.SendID) + "/events"
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}
	return c.get(ctx, path)
}

// GetContactEngagement returns per-list engagement counters for one
// contact. idOrEmail accepts a contact id (c_*) or email. listID,
// when non-empty, narrows to one list (accepts id or slug).
func (c *Client) GetContactEngagement(ctx context.Context, idOrEmail, listID string) (map[string]any, error) {
	if idOrEmail == "" {
		return nil, errors.New("minigun: idOrEmail is required")
	}
	path := "/contacts/" + enc(idOrEmail) + "/engagement"
	if listID != "" {
		path += "?list_id=" + url.QueryEscape(listID)
	}
	return c.get(ctx, path)
}

// PruneListArgs describes a list-hygiene run. At least one of the three
// criteria fields must be > 0 — pruning a list with no criteria would
// match every contact and is rejected server-side.
type PruneListArgs struct {
	List                       string
	MinMessagesSinceEngagement int64 // messages_since_last_engagement >= N
	DormantForDays             int64 // last open/click older than D days
	NoDeliveryForDays          int64 // never delivered in last D days
	// DryRun=nil defaults to TRUE on the wire (server-side default). Set
	// *bool=&false explicitly to commit. The two-step ergonomics are
	// intentional: callers can't accidentally purge a list by forgetting
	// to set a field.
	DryRun     *bool
	Limit      int
	SampleSize int
}

// PruneList unsubscribes dormant contacts from a list based on
// engagement signals from the events archive. Requires Phase 2
// (events archive) data on the server.
//
// DryRun=nil → server defaults to dry_run=true. Set DryRun to &false
// explicitly to commit.
//
// Returns: {list_id, dry_run, candidates, unsubscribed, sample, reason_counts}.
func (c *Client) PruneList(ctx context.Context, a PruneListArgs) (map[string]any, error) {
	if a.List == "" {
		return nil, errors.New("minigun: List is required")
	}
	if a.MinMessagesSinceEngagement <= 0 && a.DormantForDays <= 0 && a.NoDeliveryForDays <= 0 {
		return nil, errors.New("minigun: at least one of MinMessagesSinceEngagement, DormantForDays, NoDeliveryForDays must be > 0")
	}
	body := map[string]any{
		"min_messages_since_engagement": a.MinMessagesSinceEngagement,
		"dormant_for_days":              a.DormantForDays,
		"no_delivery_for_days":          a.NoDeliveryForDays,
	}
	if a.DryRun != nil {
		body["dry_run"] = *a.DryRun
	}
	if a.Limit > 0 {
		body["limit"] = a.Limit
	}
	if a.SampleSize > 0 {
		body["sample_size"] = a.SampleSize
	}
	return c.post(ctx, "/lists/"+enc(a.List)+"/prune", body)
}

// ---------------------------------------------------------------------
// Transport
// ---------------------------------------------------------------------

func (c *Client) get(ctx context.Context, path string) (map[string]any, error) {
	return c.do(ctx, http.MethodGet, path, nil)
}

func (c *Client) post(ctx context.Context, path string, body any) (map[string]any, error) {
	return c.do(ctx, http.MethodPost, path, body)
}

func (c *Client) do(ctx context.Context, method, path string, body any) (map[string]any, error) {
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("minigun: marshal body: %w", err)
		}
		reader = bytes.NewReader(buf)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return nil, &TransportError{Err: err}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, &TransportError{Err: err}
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &TransportError{Err: err}
	}

	var decoded any
	if len(raw) > 0 {
		// Best-effort JSON decode; non-JSON payloads (e.g. a stray
		// HTML 502 from an edge) flow through to APIError untouched.
		if jerr := json.Unmarshal(raw, &decoded); jerr != nil {
			decoded = string(raw)
		}
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := ""
		if m, ok := decoded.(map[string]any); ok {
			if e, ok := m["error"].(string); ok {
				msg = e
			}
		}
		if msg == "" {
			if s, ok := decoded.(string); ok {
				msg = s
			} else {
				msg = http.StatusText(resp.StatusCode)
			}
		}
		return nil, &APIError{Status: resp.StatusCode, Body: decoded, Message: msg}
	}

	if m, ok := decoded.(map[string]any); ok {
		return m, nil
	}
	return map[string]any{}, nil
}

// ---------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------

func enc(s string) string { return url.PathEscape(s) }

// resolveBody returns whichever of `direct` or the contents of
// `file` is non-empty. Returns an error if both are non-empty, or
// the file is unreadable. Returns "" (no error) when neither is set;
// the caller decides whether that's required.
func resolveBody(name, direct, file string) (string, error) {
	if direct != "" && file != "" {
		return "", fmt.Errorf("minigun: pass only one of %s or %sFile, not both", name, name)
	}
	if file == "" {
		return direct, nil
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return "", fmt.Errorf("minigun: read %sFile %q: %w", name, file, err)
	}
	return string(b), nil
}
