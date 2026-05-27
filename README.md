# MiniGun

A tiny self-hosted email sender on top of [Mailgun](https://www.mailgun.com). Author your emails in **Markdown**, drive it from a **CLI** or an **AI client over MCP**, and deploy it **straight to the Cloudflare edge** — no Redis, no queue service, no long-running process required. Also packaged as a single Go binary or a Docker container if you'd rather host it yourself.

```bash
go install github.com/ranaroussi/minigun/cli/cmd/minigun@latest

export MINIGUN_API_URL=https://mailer.example.com
export MINIGUN_API_TOKEN=...

minigun list create  --name "Weekly" --slug weekly
minigun contact add  weekly alice@example.com --params '{"first_name":"Alice"}'

cat > week-12.md <<'EOF'
Hi {{first_name | "there"}},

Big news this week — here's the [full update](https://example.com/blog/12).

Cheers,
Ran
EOF

minigun send bulk --list weekly --subject "Weekly update" \
  --from "Ran <ran@example.com>" --md ./week-12.md
```

## Why MiniGun

Mailgun is excellent at sending email. It is not opinionated about how you store contacts, how recipients unsubscribe, or how you resume a bulk send after a crash — those are your problem. MiniGun is the thin layer that solves them and stays out of the way.

**Mailgun keeps owning what it's good at:** delivery, open/click tracking, deliverability, bounce handling, reputation, IP warmup.

**MiniGun owns the parts you'd otherwise glue together by hand:**

- Contacts and lists, in SQLite (or D1), with arbitrary per-contact JSON metadata.
- Per-recipient unsubscribe links you actually own — stateless HMAC tokens, no DB lookup to verify, no Mailgun suppression-list lock-in.
- A two-step CAPTCHA-protected unsubscribe page so email security scanners (Microsoft Defender, Gmail link inspection, Apple Mail Privacy Protection, etc.) don't silently unsubscribe your recipients by pre-fetching the link.
- RFC 2369 + RFC 8058 one-click unsubscribe headers on every bulk send (required by Gmail and Yahoo's bulk-sender rules).
- Crash-safe bulk sends. Each batch is checkpointed by a monotonic subscription id; if the process dies mid-send you resume from where you left off. Recipients added during a send don't get pulled in mid-flight.
- Companies / brands group related lists so a recipient on the *Acme Newsletter* and *Acme Product Updates* lists sees a single combined preferences page.
- Permanent per-send stats. Mailgun retains event logs for only 5 days; MiniGun pulls the Metrics API on a front-loaded schedule (+0, +1h, +6h, +24h, +48h, +5d after completion) and persists the aggregates locally.
- Clean Gmail rendering on cross-domain `From` headers. MiniGun always sets `Sender: <From>` so Mailgun doesn't rewrite it to a VERP bounce address, which is what makes Gmail show `From: brand@example.com via mailgun-route.example.com` and hide the native one-click unsubscribe.
- Dry-run sends. `minigun send bulk --testmode` (or `send single --testmode`) runs the full pipeline through to Mailgun with `o:testmode=yes`: the message is accepted and logged but not delivered. The flag is persisted on the send row, so cron-resumed chains keep it set for every subsequent batch.
- Automatic list hygiene. A Mailgun-signed webhook auto-removes hard-bouncing addresses and spam-complainers from every list, in real time — no cron job, no manual cleanup, no third-party suppression service. Bounce purges the contact + all subscriptions; complaint also writes a permanent audit row so the address can be filtered out of future imports. HMAC-verified, replay-bounded, idempotent against Mailgun's retries.

The philosophy:

> Keep sending simple. Let Mailgun do the heavy lifting. Own your contacts and unsubscribe flow.

## What makes MiniGun different

### Bulk email on the Cloudflare edge

The same HTTP API, the same database schema, the same HMAC token format as the Go server — but as a Cloudflare Worker backed by D1. **No long-running process.** The bulk-send loop is a chain of self-invoking HTTP requests guarded by an atomic D1 batch claim: each step processes one batch and `ctx.waitUntil(fetch(/next))`s the next. A once-a-minute cron sweeps any send whose chain has gone quiet, so even a worker crash recovers without your involvement. The same cron refreshes per-send stats on a front-loaded schedule so historical sends keep their numbers forever.

Tokens signed by the Go server verify on the Worker, and vice-versa: both sides use the same HMAC-SHA256 wire format over `crypto/hmac` and Web Crypto. You can run them side-by-side, or migrate in either direction, without invalidating a single unsubscribe link.

→ [docs/cloudflare.md](./docs/cloudflare.md)

### A first-class CLI

A single `minigun` command — installable with one `go install` — that exposes every server operation with sensible flags, JSON I/O, and a `--watch` mode for tailing in-flight sends.

```bash
minigun health
minigun list    create     --name "Weekly" --slug weekly
minigun contact add        weekly ran@example.com --params '{"first_name":"Ran"}'
minigun contact delete     bounced@example.com    # hard-bounce purge (or by c_xxxx id)
minigun send    bulk       --list weekly --subject "Hi" --from "Ran <r@x.com>" --md ./email.md
minigun send    status     s_xxxx --watch
minigun send    stats      s_xxxx
minigun send    resume     s_xxxx          # crash-safe; --force after an in-flight batch
```

Configuration is just two env vars (`MINIGUN_API_URL`, `MINIGUN_API_TOKEN`) so it slots into whatever shell/CI you already use.

→ [docs/cli.md](./docs/cli.md)

### MCP server — drive it with an AI

The exact same `minigun` binary doubles as a [Model Context Protocol](https://modelcontextprotocol.io) server over stdio. Every CLI operation is exposed as an MCP tool; lists, contacts, and sends as MCP resources. Built on the official Go MCP SDK.

Wire it into Claude Desktop, Cursor, Zed, Continue, Goose, or anything else that speaks MCP:

```json
{
  "mcpServers": {
    "minigun": {
      "command": "minigun",
      "args": ["mcp"],
      "env": {
        "MINIGUN_API_URL": "https://mailer.example.com",
        "MINIGUN_API_TOKEN": "..."
      }
    }
  }
}
```

Then ask your model in plain English:

> *"How did last Tuesday's send to the 'weekly' list perform? Draft a follow-up to anyone who opened it but didn't click."*

Destructive tools (`send_bulk`, `send_single`, `unsubscribe_contact`, `resume_send`) are tagged so the client renders an explicit confirmation prompt. Two built-in **prompts** (`compose_newsletter`, `audit_send`) encode the two most common operator workflows.

→ [docs/cli.md](./docs/cli.md#mcp-server-minigun-mcp)

### Markdown-first authoring

Write your email in plain Markdown:

```markdown
Hi {{first_name | "there"}},

Big news this week — here's the [full update](https://example.com/blog/12).

Cheers,
Ran
```

MiniGun renders it to both HTML and plain text, rewrites `{{first_name | "there"}}`-style placeholders into Mailgun recipient variables, and — if you didn't include one — auto-injects an unsubscribe footer in both versions that resolves to your per-recipient HMAC token:

```text
HTML: <p>&nbsp;<br><a href="{{unsubscribe}}">Unsubscribe</a></p>
Text: Unsubscribe:
      {{unsubscribe}}
```

No MJML. No HTML email builder. No second template language. Just Markdown.

### Automatic list hygiene

The single biggest reason newsletter senders trash their reputation is mailing addresses that no longer exist (hard bounces) or actively flagged the previous send as spam. MiniGun handles both automatically.

`POST /webhooks/mailgun` is a Mailgun webhook endpoint that listens for the two events that matter:

| Event | Action |
|---|---|
| `failed` + `severity=permanent` (hard bounce) | Contact + every subscription on every list, **purged**. |
| `complained` (spam complaint / FBL) | Contact + every subscription **purged**, AND a permanent row is written to `complaint_events` so the address can be filtered out of future CSV imports. |
| `failed` + `severity=temporary` (soft bounce) | No-op. Mailgun is already retrying on the SMTP side. |
| `delivered`, `opened`, `clicked`, anything else | No-op 200. |

Every payload is HMAC-verified before we look at the body. The scheme matches Mailgun's reference exactly:

```
expected = hex(HMAC_SHA256(signing_key, timestamp + token))
accept   = constant-time-equal(expected, payload.signature) ∧ |now - timestamp| < 15min
```

Without `MAILGUN_WEBHOOK_SIGNING_KEY` configured, the endpoint **fails closed**: every request is `401`. So setup is one secret + a registration in Mailgun's dashboard (Sending → Webhooks):

```bash
# Worker:
echo 'paste-mailgun-http-webhook-signing-key-here' \
  | wrangler secret put MAILGUN_WEBHOOK_SIGNING_KEY

# Binary:
export MAILGUN_WEBHOOK_SIGNING_KEY=...
```

Then register `https://your-domain/webhooks/mailgun` for "Permanent Failure" and "Spam Complaints" on each sending domain.

Idempotent at every layer:
- `complaint_events.mailgun_event_id` is `UNIQUE` + `INSERT OR IGNORE` → webhook retries deduplicate at the storage layer.
- `DeleteContact` returns 200 `already-gone` (not 500) when the contact's already been purged by a prior delivery → Mailgun stops retrying instead of looping for 8 hours.
- HMAC verification is constant-time. Timestamps in the future are rejected as aggressively as stale ones.

The same hard-purge that the webhook performs is also available as a first-class operation across all three surfaces, for when you need to script bounce cleanup yourself (e.g. importing a list of hard bounces from a previous provider, or pruning a stale segment by hand):

```bash
# HTTP:
curl -X DELETE -H "Authorization: Bearer $MINIGUN_API_TOKEN" \
     https://your-domain/contacts/bounced@example.com
# or:
curl -X DELETE -H "Authorization: Bearer $MINIGUN_API_TOKEN" \
     https://your-domain/contacts/c_PP5AA3MBXS

# CLI:
minigun contact delete bounced@example.com
minigun contact delete c_PP5AA3MBXS

# MCP (from any client — Claude, Cursor, Zed, etc.):
#   tool: delete_contact
#   args: { "id_or_email": "bounced@example.com" }
```

The endpoint accepts either the contact id (`c_*`) or the email address, and returns the deleted contact + how many subscriptions were removed. Same semantics as the webhook path — full purge of the contact + all subscriptions + unsubscribe-event audit rows in a single transaction.

Distinct from `contact unsubscribe`, which preserves the subscription row with `subscribed=0` (correct for user-initiated opt-outs — you want to remember they opted out so a future re-import doesn't silently re-subscribe them).

## Architecture

```
your app  ──┐
            ├──►  MiniGun API  ──►  SQLite or D1
CLI / MCP ──┘            │
                         ▼
                       Mailgun API  (delivery, tracking, deliverability)
```

One HTTP service, two implementations (Go binary, Cloudflare Worker), one shared schema, one shared token format. Bulk sends are always async: `POST /send/bulk` returns a `send_id` immediately while a background loop (goroutine in the Go server, self-invoking HTTP chain in the Worker) drives the batches forward.

## Deploy

| Target | When to pick it | Walkthrough |
|---|---|---|
| **Cloudflare Worker + D1** | Zero infra; bulk sends survive crashes because there is no process to crash. | [docs/cloudflare.md](./docs/cloudflare.md) |
| Go binary (systemd / bare-metal) | On-prem or single-VM; want a real long-running process. | [docs/binary.md](./docs/binary.md) |
| Docker / Compose | Containerised stack; one-line `docker run`. | [docs/docker.md](./docs/docker.md) |

Then install the CLI + MCP on your laptop:

```bash
go install github.com/ranaroussi/minigun/cli/cmd/minigun@latest
```

## SDKs

Single-file, zero-dependency drop-in clients for the most common server languages. Every SDK exposes the same surface (contacts, sends, status, stats, resume, delete) with the same error model — pick whichever fits your stack:

| Language | File | Drop in / install | Reference |
|---|---|---|---|
| **PHP** (7.4+) | [`sdks/php/minigun.php`](./sdks/php/minigun.php) | `require_once 'minigun.php';` | [sdks/php/README.md](./sdks/php/README.md) |
| **Python** (3.9+) | [`sdks/python/minigun.py`](./sdks/python/minigun.py) | drop the file in, `from minigun import Minigun` | [sdks/python/README.md](./sdks/python/README.md) |
| **TypeScript** (ES2020+) | [`sdks/typescript/minigun.ts`](./sdks/typescript/minigun.ts) | drop the file in, `import { Minigun } from './minigun'` | [sdks/typescript/README.md](./sdks/typescript/README.md) |
| **Go** (1.21+) | [`sdks/go/minigun.go`](./sdks/go/minigun.go) | `go get github.com/ranaroussi/minigun/sdks/go` | [sdks/go/README.md](./sdks/go/README.md) |

All four are stdlib-only — no `requests`, no `axios`, no `guzzle`, no external Go modules — so they slot into any project without touching its dependency tree. The TypeScript SDK uses the standard `fetch` API, so it runs unchanged on Node 18+, Bun, Deno, Cloudflare Workers, and the browser.

See [sdks/README.md](./sdks/README.md) for the cross-language overview and the matching method-name table.

## API

The server speaks JSON over HTTP on `:8080`. When `MINIGUN_API_TOKEN` is set, all routes require `Authorization: Bearer <token>` except `/healthz`, `/u/{token}`, `/manage/{token}`, and `/webhooks/*` (the unsubscribe / manage routes carry their own HMAC token in the URL; the webhook routes are HMAC-verified per-request against the Mailgun signing key).

| Method | Path                                       | Purpose |
|--------|--------------------------------------------|---------|
| GET    | `/healthz`                                 | Health probe (DB ping). |
| GET    | `/companies`                               | List all companies. |
| POST   | `/companies`                               | Create a company. |
| GET    | `/companies/{company}`                     | One company by id or slug. |
| GET    | `/companies/{company}/lists`               | All lists belonging to a company. |
| GET    | `/lists`                                   | List all lists with `subscribed_count`. |
| POST   | `/lists`                                   | Create a list (optionally bound to a company). |
| GET    | `/lists/{list}`                            | One list with `subscribed_count`, `total_count`, `last_send_at`. |
| GET    | `/lists/{list}/contacts?cursor=&limit=`    | Paginated contacts for a list. |
| POST   | `/lists/{list}/contacts`                   | Upsert contact + subscription. |
| POST   | `/lists/{list}/unsubscribe`                | Admin unsubscribe by email (keeps row, marks `subscribed=0`). |
| DELETE | `/contacts/{idOrEmail}`                    | Hard-delete a contact + all subscriptions + audit rows (hard-bounce cleanup). |
| GET    | `/sends?cursor=&limit=`                    | Paginated send history (created_at desc). |
| POST   | `/send/bulk`                               | Start a bulk send. |
| POST   | `/send/single`                             | Send a single transactional email. |
| POST   | `/send/{id}/next`                          | Execute the next batch step (chain self-call; alias of `/resume`). |
| POST   | `/send/{id}/resume`                        | Resume a paused / failed send (alias of `/next`). |
| GET    | `/send/{id}`                               | Send status + progress. |
| GET    | `/send/{id}/stats`                         | Aggregate stats (DB-backed; falls back to live Mailgun for fresh sends). |
| GET    | `/u/{token}`                               | Render the unsubscribe confirmation page. |
| POST   | `/u/{token}`                               | Perform the unsubscribe (form post or RFC 8058 one-click). |
| GET    | `/manage/{token}`                          | Render the combined company-wide preferences page. |
| POST   | `/manage/{token}`                          | Apply preference deltas across the company's lists. |
| POST   | `/webhooks/mailgun`                        | HMAC-verified Mailgun webhook: auto-purge on hard bounce / spam complaint. |

`{list}` and `{company}` accept either id or slug. Listing endpoints use opaque base64 cursors; default `limit` is 50, max 500.

## Configuration

| Env var                        | Required | Default                  | Purpose |
|--------------------------------|----------|--------------------------|---------|
| `MAILGUN_API_KEY`              | yes      | —                        | Mailgun API key (HTTP Basic password; user is `api`). |
| `MAILGUN_REGION`               | no       | `us`                     | `us` or `eu`. |
| `MAILGUN_API_BASE`             | no       | derived from region      | Explicit override for the API base URL. |
| `MINIGUN_PUBLIC_URL`           | yes      | —                        | Public origin used to build per-recipient unsubscribe URLs. |
| `MINIGUN_HMAC_SECRET`          | yes      | —                        | Secret used to HMAC-sign unsubscribe / manage tokens. |
| `MINIGUN_API_TOKEN`            | no       | —                        | Bearer token required on every API request when set. |
| `MINIGUN_DB_PATH`              | no       | `/data/minigun.db`       | SQLite file path (Go server only). |
| `MINIGUN_LISTEN_ADDR`          | no       | `:8080`                  | HTTP listen address (Go server only). |
| `MINIGUN_TURNSTILE_SITE_KEY`   | no       | —                        | Cloudflare Turnstile site key. |
| `MINIGUN_TURNSTILE_SECRET_KEY` | no       | —                        | Turnstile secret. Required when site key is set. |
| `MAILGUN_WEBHOOK_SIGNING_KEY`  | no       | —                        | Mailgun "HTTP webhook signing key" (Sending → Webhooks). When set, `/webhooks/mailgun` accepts signed bounce/complaint events and auto-purges contacts. When unset, the endpoint refuses all requests. |

Per-deployment specifics (D1 binding for the Worker, secrets vs vars, etc.) live in the install docs.

## Development

```bash
cd src    && go build ./...     && go test ./... && go vet ./...
cd cli    && go build ./...     && go test ./...
cd worker && npm install        && npx tsc --noEmit && npx wrangler dev
```

## Project layout

```
.
├── README.md
├── LICENSE
├── docs/                   # install + CLI + MCP walkthroughs
│   ├── cloudflare.md
│   ├── cli.md
│   ├── binary.md
│   └── docker.md
├── Dockerfile              # multi-stage Alpine build for the Go server
├── docker-compose.yml      # example service-style deployment
├── src/                    # the Go server (module: github.com/ranaroussi/minigun)
│   ├── main.go
│   ├── cmd/
│   └── internal/
│       ├── api/            # chi handlers
│       ├── db/             # SQLite + embedded goose migrations
│       ├── mailgun/        # Mailgun client (messages + Metrics API)
│       ├── render/         # markdown → HTML/text, variable rewriter
│       ├── store/          # SQLite repository layer
│       ├── tmpl/           # embedded unsubscribe.html / manage.html
│       ├── token/          # HMAC unsubscribe tokens
│       ├── turnstile/      # Cloudflare Turnstile siteverify
│       └── worker/         # bulk send worker + stats refresher
├── cli/                    # standalone CLI + MCP (module: github.com/ranaroussi/minigun/cli)
│   ├── go.mod
│   ├── cmd/
│   │   ├── minigun/main.go # `go install` entry point — produces the `minigun` binary
│   │   └── *.go            # cobra commands
│   └── internal/
├── worker/                 # Cloudflare Worker port (TypeScript + Hono + D1 + Web Crypto)
│   ├── wrangler.toml
│   ├── migrations/         # D1 migrations mirroring the Go server's goose migrations
│   └── src/
└── sdks/                   # Single-file, zero-dep client SDKs (one per language)
    ├── php/minigun.php
    ├── python/minigun.py
    ├── typescript/minigun.ts
    └── go/{go.mod,minigun.go}
```

## License

[MIT](./LICENSE) © 2025 Paperclip AI.
