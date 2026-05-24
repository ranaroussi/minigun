package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

func New(baseURL, token string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   token,
		HTTP:    &http.Client{Timeout: 60 * time.Second},
	}
}

type Response struct {
	Status int
	Body   []byte
}

func (r *Response) OK() bool { return r.Status >= 200 && r.Status < 300 }

func (r *Response) Error() error {
	if r.OK() {
		return nil
	}
	msg := tryDecodeError(r.Body)
	if msg == "" {
		msg = strings.TrimSpace(string(r.Body))
	}
	if msg == "" {
		return fmt.Errorf("server returned %d", r.Status)
	}
	return fmt.Errorf("server returned %d: %s", r.Status, msg)
}

func tryDecodeError(body []byte) string {
	var v struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &v); err == nil && v.Error != "" {
		return v.Error
	}
	return ""
}

func (c *Client) Do(method, path string, body any) (*Response, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode body: %w", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, c.BaseURL+path, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "minigun-cli/0.1")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request to %s: %w", c.BaseURL+path, err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return &Response{Status: resp.StatusCode, Body: out}, nil
}

func (c *Client) Get(path string) (*Response, error) {
	return c.Do(http.MethodGet, path, nil)
}

func (c *Client) Post(path string, body any) (*Response, error) {
	return c.Do(http.MethodPost, path, body)
}
