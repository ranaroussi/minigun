# MiniGun — Go SDK

Idiomatic Go client for [MiniGun](https://github.com/ranaroussi/minigun). Stdlib-only (no external dependencies in `go.mod`), published as a nested module so you can pull it in without dragging in the server, CLI, or worker code.

**Requires:** Go 1.21+.

## Install

```bash
go get github.com/ranaroussi/minigun/sdks/go
```

Then:

```go
import "github.com/ranaroussi/minigun/sdks/go"
```

The package name is `minigun` (the directory is `sdks/go` for layout only).

## Quickstart

```go
package main

import (
    "context"
    "errors"
    "fmt"
    "log"
    "os"

    "github.com/ranaroussi/minigun/sdks/go"
)

func main() {
    ctx := context.Background()

    mg, err := minigun.New(
        os.Getenv("MINIGUN_API_URL"),
        minigun.WithToken(os.Getenv("MINIGUN_API_TOKEN")),
    )
    if err != nil {
        log.Fatal(err)
    }

    // Upsert a contact and subscribe them.
    if _, err := mg.AddContact(ctx, "newsletter", "alice@example.com",
        map[string]any{"first_name": "Alice"}); err != nil {
        log.Fatal(err)
    }

    // Send a bulk campaign.
    res, err := mg.SendBulk(ctx, minigun.SendBulkArgs{
        List:    "newsletter",
        Subject: "Big news this week",
        From:    "Ran <ran@example.com>",
        MDFile:  "./week-12.md",
    })
    if err != nil {
        var apiErr *minigun.APIError
        if errors.As(err, &apiErr) {
            log.Fatalf("API %d: %s", apiErr.Status, apiErr.Message)
        }
        log.Fatal(err)
    }

    fmt.Printf("queued send %s — %v recipients\n",
        res["send_id"], res["total_recipients"])
}
```

## Reference

### Construction

```go
func New(baseURL string, opts ...Option) (*Client, error)
```

`baseURL` is the API origin (e.g. `https://mailer.example.com`); trailing slash optional. Options:

| Option | Effect | Default |
|---|---|---|
| `WithToken(t string)`        | Bearer token sent on every request.                          | none — required if server has `MINIGUN_API_TOKEN` set |
| `WithTimeout(d time.Duration)` | Overall request timeout on the default `*http.Client`.     | `120 * time.Second` |
| `WithHTTPClient(h *http.Client)` | Replace the entire HTTP client (custom transport, tracing, mTLS, retries via go-retryablehttp, etc). | stdlib `&http.Client{Timeout: 120s}` |
| `WithUserAgent(ua string)`   | UA string for server-side log attribution.                   | `minigun-go/0.1` |

Functional options is the idiomatic Go pattern for optional construction params — it gives you forward compatibility (new options added without breaking callers) and keeps the zero-option case clean.

### Contacts

```go
func (c *Client) AddContact(ctx context.Context, list, email string, params map[string]any) (map[string]any, error)
func (c *Client) UnsubscribeContact(ctx context.Context, list, email string) (map[string]any, error)
func (c *Client) DeleteContact(ctx context.Context, idOrEmail string) (map[string]any, error)
func (c *Client) ListContacts(ctx context.Context, list, cursor string, limit int) (map[string]any, error)
```

- **`AddContact`** — Upsert. Safe to call repeatedly: existing params are merged and any prior unsubscribe is cleared.
- **`UnsubscribeContact`** — Admin-side opt-out. Preserves the row with `subscribed=0` so future re-imports don't silently re-subscribe. Use this for user-initiated unsubscribes.
- **`DeleteContact`** — Hard purge: removes the contact + every subscription + every audit row. Use this for hard-bounce cleanup. The Mailgun webhook (`/webhooks/mailgun`) does this automatically; this method is for scripted / one-off purges. Accepts either `c_XXXXXXXXXX` ids or email addresses.
- **`ListContacts`** — Paginated. Pass `cursor=""` for the first page; pass `limit<=0` to use the default of 50.

### Sends

```go
func (c *Client) SendSingle(ctx context.Context, args SendSingleArgs) (map[string]any, error)
func (c *Client) SendBulk(ctx context.Context, args SendBulkArgs) (map[string]any, error)
func (c *Client) GetSend(ctx context.Context, sendID string) (map[string]any, error)
func (c *Client) GetSendStats(ctx context.Context, sendID string) (map[string]any, error)
func (c *Client) ResumeSend(ctx context.Context, sendID string, force bool) (map[string]any, error)
```

`SendSingleArgs` and `SendBulkArgs` are exported structs with zero-value defaults — set only the fields you need:

```go
res, err := mg.SendBulk(ctx, minigun.SendBulkArgs{
    List:     "newsletter",
    Subject:  "Weekly update",
    From:     "Ran <ran@example.com>",
    MDFile:   "./week-12.md",
    TestMode: true,
})
```

For each body field there's a value-or-file pair:

| Direct (string) | File path (string) |
|---|---|
| `MD`       | `MDFile`       |
| `HTML`     | `HTMLFile`     |
| `Text`     | `TextFile`     |
| `Template` | `TemplateFile` |

Pass at most one of each pair — passing both returns an error before any HTTP call. Files are read synchronously via `os.ReadFile`.

Zero-value defaults you'll likely want to know:

| Field | Zero value behavior |
|---|---|
| `BatchSize`  | `0` → `500` |
| `ThrottleMs` | `0` → `1000` |
| `UnsubMode`  | `""` → `UnsubLocal` |

Unsubscribe-mode constants:

| Mode | Constant | When to use | Required extra field |
|---|---|---|---|
| Local unsubscribe page | `minigun.UnsubLocal` (default) | Standard. Renders the MiniGun unsub / preferences page. | — |
| Redirect after unsub | `minigun.UnsubRedirect` | Send the user to your own thank-you page after they opt out. | `UnsubRedir` |
| External (your own) | `minigun.UnsubExternal` | You host the entire unsub flow on your own domain. | `UnsubURL` |

### Errors

```go
res, err := mg.SendBulk(ctx, args)
if err != nil {
    var apiErr *minigun.APIError
    var transportErr *minigun.TransportError

    switch {
    case errors.As(err, &apiErr):
        // Server returned 4xx/5xx.
        log.Printf("API %d: %s (body=%v)", apiErr.Status, apiErr.Message, apiErr.Body)
    case errors.As(err, &transportErr):
        // Network failure, timeout, ctx cancelled.
        log.Printf("transport: %v", transportErr.Err)
    default:
        // Local validation: mutually-exclusive fields, unknown unsub mode,
        // unreadable file path, etc.
        log.Printf("invalid args: %v", err)
    }
}
```

Both `*APIError` and `*TransportError` implement `error` and `Unwrap`, so `errors.As` / `errors.Is` work as expected. The split matters for retry policy: transport errors are often worth retrying with backoff; API errors usually aren't.

## Context, cancellation, deadlines

Every method takes `context.Context` as the first argument. Use it for:

- **Deadlines** — `ctx, cancel := context.WithTimeout(ctx, 30*time.Second); defer cancel()`. Wins over `WithTimeout(...)` when you want per-call rather than per-client.
- **Cancellation** — propagate request-scoped cancellation (e.g. an inbound HTTP handler whose client disconnected). The SDK passes the context through to `http.NewRequestWithContext`, so any in-flight request is aborted promptly.

## Concurrency

`*Client` is safe for concurrent use. The underlying `*http.Client` is goroutine-safe, and the SDK never mutates client state after construction. One `*Client` per service is the right shape — share it via dependency injection.

## See also

- [Top-level README](../../README.md) — server install, deployment, full HTTP API reference.
- [Cross-SDK overview](../README.md) — method-name table across all four languages.
- [Auto list hygiene](../../README.md#automatic-list-hygiene) — the Mailgun webhook is server-side and needs no SDK code; the `DeleteContact` method here is the manual / scripted equivalent.
