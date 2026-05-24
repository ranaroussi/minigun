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
	"net/mail"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	APIBase string
	Domain  string
	APIKey  string
	HTTP    *http.Client
}

func New(apiBase, domain, apiKey string) *Client {
	return &Client{
		APIBase: strings.TrimRight(apiBase, "/"),
		Domain:  domain,
		APIKey:  apiKey,
		HTTP:    &http.Client{Timeout: 60 * time.Second},
	}
}

func (c *Client) sendingDomain(from string) (string, error) {
	if c.Domain != "" {
		return c.Domain, nil
	}
	addr, err := mail.ParseAddress(from)
	if err != nil {
		return "", fmt.Errorf("MAILGUN_DOMAIN is unset and cannot derive sending domain from From header %q: %w", from, err)
	}
	at := strings.LastIndex(addr.Address, "@")
	if at < 0 || at == len(addr.Address)-1 {
		return "", fmt.Errorf("MAILGUN_DOMAIN is unset and From header %q has no @host part", from)
	}
	return addr.Address[at+1:], nil
}

type Message struct {
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
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)

	add := func(k, v string) {
		_ = mw.WriteField(k, v)
	}

	add("from", m.From)
	for _, to := range m.To {
		add("to", to)
	}
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
	if err := mw.Close(); err != nil {
		return nil, err
	}

	domain, err := c.sendingDomain(m.From)
	if err != nil {
		return nil, err
	}
	endpoint := fmt.Sprintf("%s/v3/%s/messages", c.APIBase, domain)
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
		Start:      mr.Start.UTC().Format(time.RFC3339),
		End:        mr.End.UTC().Format(time.RFC3339),
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

func ParseRetryAfter(h string) time.Duration {
	if h == "" {
		return 0
	}
	if secs, err := strconv.Atoi(h); err == nil {
		return time.Duration(secs) * time.Second
	}
	return 0
}
