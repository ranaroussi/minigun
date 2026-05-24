package turnstile

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const verifyURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

type Verifier struct {
	Secret string
	HTTP   *http.Client
}

func New(secret string) *Verifier {
	return &Verifier{
		Secret: secret,
		HTTP:   &http.Client{Timeout: 5 * time.Second},
	}
}

type Result struct {
	Success     bool      `json:"success"`
	ChallengeTS time.Time `json:"challenge_ts"`
	Hostname    string    `json:"hostname"`
	ErrorCodes  []string  `json:"error-codes"`
	Action      string    `json:"action"`
	CData       string    `json:"cdata"`
}

func (v *Verifier) Verify(ctx context.Context, token, remoteIP string) (*Result, error) {
	if v == nil || v.Secret == "" {
		return &Result{Success: true}, nil
	}
	form := url.Values{}
	form.Set("secret", v.Secret)
	form.Set("response", token)
	if remoteIP != "" {
		form.Set("remoteip", remoteIP)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, verifyURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := v.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("turnstile request: %w", err)
	}
	defer resp.Body.Close()
	var out Result
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("turnstile decode: %w", err)
	}
	return &out, nil
}
