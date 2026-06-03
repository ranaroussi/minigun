package mailgun

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	APIBase string
	APIKey  string
	HTTP    *http.Client
}

func New(apiBase, apiKey string) *Client {
	return &Client{
		APIBase: strings.TrimRight(apiBase, "/"),
		APIKey:  apiKey,
		HTTP:    &http.Client{Timeout: 60 * time.Second},
	}
}

type Message struct {
	Domain  string
	From    string
	To      []string
	Subject string
	HTML    string
	Text    string
	ReplyTo string

	Tag string

	TrackingOpens         bool
	TrackingClicks        bool
	TrackingUnsubscribeOn bool

	ListUnsubscribe     string
	ListUnsubscribePost string

	RecipientVariables map[string]map[string]any

	CustomVars map[string]string

	TestMode bool
}

type SendResponse struct {
	ID      string `json:"id"`
	Message string `json:"message"`
}

type APIError struct {
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("mailgun api error: status=%d body=%s", e.StatusCode, e.Body)
}

func (e *APIError) Retryable() bool {
	return e.StatusCode == http.StatusTooManyRequests || (e.StatusCode >= 500 && e.StatusCode <= 599)
}

func (c *Client) SendMessage(ctx context.Context, m *Message) (*SendResponse, error) {
	if m.Domain == "" {
		return nil, fmt.Errorf("mailgun: message.Domain is required")
	}
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)

	add := func(k, v string) {
		_ = mw.WriteField(k, v)
	}

	add("from", m.From)
	for _, to := range m.To {
		add("to", to)
	}
	// Explicitly set the RFC 5322 Sender header to match From. Without this,
	// when From.domain differs from the sending domain Mailgun synthesizes a
	// VERP-style Sender (e.g. "user=apex.com@subdomain.com") for bounce
	// routing and Gmail/Apple Mail surface it in the visible message UI.
	// RFC 5322 §3.6.2 says when Sender == From, well-behaved clients may
	// omit Sender from display entirely, so this is harmless even when the
	// domains already align.
	add("h:Sender", m.From)
	add("subject", m.Subject)
	if m.Text != "" {
		add("text", m.Text)
	}
	if m.HTML != "" {
		add("html", m.HTML)
	}
	if m.ReplyTo != "" {
		add("h:Reply-To", m.ReplyTo)
	}
	if m.Tag != "" {
		add("o:tag", m.Tag)
	}
	add("o:tracking", "yes")
	if m.TrackingOpens {
		add("o:tracking-opens", "yes")
	} else {
		add("o:tracking-opens", "no")
	}
	if m.TrackingClicks {
		add("o:tracking-clicks", "yes")
	} else {
		add("o:tracking-clicks", "no")
	}
	if m.TrackingUnsubscribeOn {
		add("o:tracking-unsubscribe", "yes")
	} else {
		add("o:tracking-unsubscribe", "no")
	}
	if m.ListUnsubscribe != "" {
		add("h:List-Unsubscribe", m.ListUnsubscribe)
	}
	if m.ListUnsubscribePost != "" {
		add("h:List-Unsubscribe-Post", m.ListUnsubscribePost)
	}
	if len(m.RecipientVariables) > 0 {
		j, err := json.Marshal(m.RecipientVariables)
		if err != nil {
			return nil, fmt.Errorf("marshal recipient-variables: %w", err)
		}
		add("recipient-variables", string(j))
	}
	for k, v := range m.CustomVars {
		add("v:"+k, v)
	}
	if m.TestMode {
		add("o:testmode", "yes")
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}

	endpoint := fmt.Sprintf("%s/v3/%s/messages", c.APIBase, m.Domain)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth("api", c.APIKey)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &APIError{StatusCode: resp.StatusCode, Body: string(respBody)}
	}
	var out SendResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return &SendResponse{Message: string(respBody)}, nil
	}
	return &out, nil
}

func (c *Client) SendMessageWithRetry(ctx context.Context, m *Message, maxAttempts int) (*SendResponse, error) {
	if maxAttempts < 1 {
		maxAttempts = 5
	}
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		resp, err := c.SendMessage(ctx, m)
		if err == nil {
			return resp, nil
		}
		var apiErr *APIError
		if errors.As(err, &apiErr) && !apiErr.Retryable() {
			return nil, err
		}
		lastErr = err
		if attempt == maxAttempts {
			break
		}
		wait := backoff(attempt)
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return nil, lastErr
}

func backoff(attempt int) time.Duration {
	base := time.Duration(1<<attempt) * time.Second
	if base > 30*time.Second {
		base = 30 * time.Second
	}
	jitter := time.Duration(rand.Int63n(int64(500 * time.Millisecond)))
	return base + jitter
}

type MetricsRequest struct {
	Start      time.Time
	End        time.Time
	Resolution string
	Metrics    []string
	Tag        string
}

type MetricsResponse struct {
	Items []MetricsItem `json:"items"`
	Raw   json.RawMessage
}

type MetricsItem struct {
	Dimensions []string          `json:"dimensions"`
	Metrics    map[string]uint64 `json:"metrics"`
}

func (c *Client) Metrics(ctx context.Context, mr MetricsRequest) (*MetricsResponse, error) {
	type filterValue struct {
		Label string `json:"label"`
		Value string `json:"value"`
	}
	type filterClause struct {
		Attribute  string        `json:"attribute"`
		Comparator string        `json:"comparator"`
		Values     []filterValue `json:"values"`
	}
	type filterAnd struct {
		AND []filterClause `json:"AND"`
	}
	type metricsBody struct {
		Start      string     `json:"start"`
		End        string     `json:"end"`
		Resolution string     `json:"resolution"`
		Metrics    []string   `json:"metrics"`
		Filter     *filterAnd `json:"filter,omitempty"`
	}

	body := metricsBody{
		// Mailgun's analytics-metrics endpoint rejects ISO-8601 (RFC3339); it
		// wants an RFC 2822 / RFC 1123 date (e.g. "Mon, 02 Jun 2026 09:58:03
		// GMT"), which is what time.RFC1123 produces in UTC.
		Start:      mr.Start.UTC().Format(time.RFC1123),
		End:        mr.End.UTC().Format(time.RFC1123),
		Resolution: defaultStr(mr.Resolution, "day"),
		Metrics:    mr.Metrics,
	}
	if mr.Tag != "" {
		body.Filter = &filterAnd{AND: []filterClause{{
			Attribute:  "tag",
			Comparator: "=",
			Values:     []filterValue{{Label: mr.Tag, Value: mr.Tag}},
		}}}
	}

	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	endpoint := fmt.Sprintf("%s/v1/analytics/metrics", c.APIBase)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth("api", c.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &APIError{StatusCode: resp.StatusCode, Body: string(respBody)}
	}
	var out MetricsResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		out.Raw = respBody
	}
	out.Raw = respBody
	return &out, nil
}

func defaultStr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// PerSendTotals are the aggregate counts Mailgun reports for a single MiniGun
// send (identified by tag = send.ID). Unsubscribed is intentionally omitted —
// MiniGun owns that count via its own unsubscribe_events table.
type PerSendTotals struct {
	Sent       uint64
	Delivered  uint64
	Opened     uint64
	Clicked    uint64
	Failed     uint64
	Complained uint64
}

func (c *Client) PerSendMetrics(ctx context.Context, sendID string, sendCreatedAt time.Time) (*PerSendTotals, error) {
	start := sendCreatedAt.Add(-1 * time.Hour)
	end := time.Now().Add(1 * time.Hour)
	mr := MetricsRequest{
		Start:      start,
		End:        end,
		Resolution: "day",
		Metrics:    []string{"accepted_count", "delivered_count", "failed_count", "opened_count", "clicked_count", "complained_count"},
		Tag:        sendID,
	}
	resp, err := c.Metrics(ctx, mr)
	if err != nil {
		return nil, err
	}
	totals := PerSendTotals{}
	if resp != nil {
		for _, item := range resp.Items {
			totals.Sent += item.Metrics["accepted_count"]
			totals.Delivered += item.Metrics["delivered_count"]
			totals.Opened += item.Metrics["opened_count"]
			totals.Clicked += item.Metrics["clicked_count"]
			totals.Failed += item.Metrics["failed_count"]
			totals.Complained += item.Metrics["complained_count"]
		}
	}
	return &totals, nil
}

// ---------------------------------------------------------------------------
// Events API
// ---------------------------------------------------------------------------

// RawEvent mirrors the subset of Mailgun's per-event JSON the engagement
// rollups consume. The forensic/variable-shape fields (message,
// client-info, geolocation, user-variables) are intentionally not decoded
// — MiniGun keeps no raw event log, only the per-(send, contact) and
// per-(contact, list) rollups folded from these fields.
type RawEvent struct {
	ID        string   `json:"id"`
	Event     string   `json:"event"`
	Timestamp float64  `json:"timestamp"`
	Recipient string   `json:"recipient"`
	Severity  string   `json:"severity,omitempty"`
	Reason    string   `json:"reason,omitempty"`
	URL       string   `json:"url,omitempty"`
	Tags      []string `json:"tags,omitempty"`
}

// EventsPage is one paginated response from Mailgun's events API. The
// Paging.Next field carries the cursor URL for the subsequent page; an
// empty Next or zero items means we've reached the end.
type EventsPage struct {
	Items  []RawEvent `json:"items"`
	Paging struct {
		Next     string `json:"next,omitempty"`
		Previous string `json:"previous,omitempty"`
		First    string `json:"first,omitempty"`
		Last     string `json:"last,omitempty"`
	} `json:"paging"`
}

// FetchEventsParams is the input to a first-page fetch. For follow-up
// pages, use FetchEventsPage with the cursor URL from the previous
// page's Paging.Next directly.
type FetchEventsParams struct {
	Domain  string
	Tag     string    // filter by o:tag (i.e. by MiniGun send_id); empty = no filter
	BeginMs int64     // 0 = no lower bound
	EndMs   int64     // 0 = no upper bound
	Limit   int       // 0 = Mailgun's default (300)
}

// FetchEvents fetches the first page of events for a domain, applying the
// tag filter and time window if set. The returned EventsPage carries the
// next-page cursor URL so the caller can paginate via FetchEventsPage.
func (c *Client) FetchEvents(ctx context.Context, p FetchEventsParams) (*EventsPage, error) {
	if p.Domain == "" {
		return nil, fmt.Errorf("mailgun: FetchEvents requires domain")
	}
	q := make([]string, 0, 6)
	q = append(q, "ascending=yes")
	limit := p.Limit
	if limit <= 0 {
		limit = 300
	}
	q = append(q, fmt.Sprintf("limit=%d", limit))
	if p.Tag != "" {
		q = append(q, "tags="+p.Tag)
	}
	// Mailgun accepts begin/end as floats (epoch seconds). Using seconds
	// rather than RFC 2822 strings is more robust across server timezones.
	if p.BeginMs > 0 {
		q = append(q, fmt.Sprintf("begin=%.3f", float64(p.BeginMs)/1000.0))
	}
	if p.EndMs > 0 {
		q = append(q, fmt.Sprintf("end=%.3f", float64(p.EndMs)/1000.0))
	}
	url := fmt.Sprintf("%s/v3/%s/events?%s", c.APIBase, p.Domain, strings.Join(q, "&"))
	return c.fetchEventsURL(ctx, url)
}

// FetchEventsPage follows a Mailgun-supplied cursor URL (page.Paging.Next)
// to fetch the next page of events.
func (c *Client) FetchEventsPage(ctx context.Context, pageURL string) (*EventsPage, error) {
	if pageURL == "" {
		return nil, fmt.Errorf("mailgun: FetchEventsPage requires pageURL")
	}
	return c.fetchEventsURL(ctx, pageURL)
}

func (c *Client) fetchEventsURL(ctx context.Context, url string) (*EventsPage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth("api", c.APIKey)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &APIError{StatusCode: resp.StatusCode, Body: string(body)}
	}
	// Decode the wrapper to items + paging, then decode each item
	// individually so a single malformed event doesn't kill the page.
	var wrapper struct {
		Items  []json.RawMessage `json:"items"`
		Paging struct {
			Next     string `json:"next,omitempty"`
			Previous string `json:"previous,omitempty"`
			First    string `json:"first,omitempty"`
			Last     string `json:"last,omitempty"`
		} `json:"paging"`
	}
	if err := json.Unmarshal(body, &wrapper); err != nil {
		return nil, fmt.Errorf("decode events page: %w", err)
	}
	page := &EventsPage{}
	page.Paging = wrapper.Paging
	for _, raw := range wrapper.Items {
		var ev RawEvent
		if err := json.Unmarshal(raw, &ev); err != nil {
			continue
		}
		page.Items = append(page.Items, ev)
	}
	return page, nil
}

func ParseRetryAfter(h string) time.Duration {
	if h == "" {
		return 0
	}
	if secs, err := strconv.Atoi(h); err == nil {
		return time.Duration(secs) * time.Second
	}
	return 0
}
