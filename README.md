# MiniGun

A tiny, self-hosted email sender on top of [Mailgun](https://www.mailgun.com). SQLite for state, single Go binary, no Redis, no Postgres, no JavaScript build step. Also ships as a Cloudflare Worker on D1.

```bash
docker run -d --name minigun --restart unless-stopped \
  -p 8080:8080 -v "$PWD/data:/data" \
  -e MAILGUN_API_KEY=key-... \
  -e MINIGUN_PUBLIC_URL=https://minigun.example.com \
  -e MINIGUN_HMAC_SECRET="$(openssl rand -hex 32)" \
  -e MINIGUN_API_TOKEN="$(openssl rand -hex 32)" \
  ghcr.io/ranaroussi/minigun:latest

go install github.com/ranaroussi/minigun/cli@latest
minigun list create --name "Weekly Newsletter" --slug newsletter
minigun contact add newsletter ran@example.com --params '{"first_name":"Ran"}'
minigun send bulk --list newsletter --subject "Weekly update" \
  --from "Ran <ran@example.com>" --md ./email.md
```

## Why MiniGun

Mailgun is excellent at sending email. It is not opinionated about how you store contacts, how you let recipients unsubscribe, or how you resume a bulk send after a crash — those are your problem. MiniGun is the thin layer that solves those problems and stays out of the way.

**Mailgun keeps owning what it's good at:** delivery, open/click tracking, deliverability, bounce handling, reputation, IP warmup, ESP analytics.

**MiniGun owns the parts you usually end up gluing together by hand:**

- Contacts and lists, stored locally in SQLite with arbitrary JSON metadata per contact.
- Per-recipient unsubscribe links you actually own — stateless HMAC tokens, no DB lookup to verify, no Mailgun suppression-list lock-in.
- A CAPTCHA-protected two-step unsubscribe page so email security scanners (Microsoft Defender, Gmail link inspection, Apple Mail Privacy Protection, etc.) don't silently unsubscribe your recipients by pre-fetching the link.
- RFC 2369 + RFC 8058 one-click unsubscribe headers, set on every bulk send. Required by Gmail and Yahoo's bulk-sender rules.
- Bulk sends with **crash-safe resume**: each batch is checkpointed by a monotonic `subscriptions.id`; if the worker crashes you simply resume from where you left off. Recipients added during a send don't get pulled in mid-flight.
- Markdown-first authoring. Author your email in Markdown; MiniGun renders to HTML and plain text and rewrites `{{first_name | "there"}}`-style placeholders into Mailgun recipient variables.
- Companies / brands group related lists under one parent so a recipient on the *Acme Newsletter* and *Acme Product Updates* lists sees a single combined preferences page, not two separate ones.
- Permanent per-send stats. Mailgun retains event logs for 5 days; MiniGun pulls Mailgun's Metrics API on a front-loaded schedule (+0, +1h, +6h, +24h, +48h, +5d after a send completes) and persists the aggregates locally, so historical sends keep their numbers forever.

**MiniGun deliberately is not:** a full ESP, a marketing-automation suite, a visual email builder, a workflow engine, a multi-provider abstraction layer. Those are different products. The philosophy is:

> Keep sending simple. Let Mailgun do the heavy lifting. Own your contacts and unsubscribe flow.

## Architecture

```
your app  ──┐
            ├──►  MiniGun API  ──►  SQLite (one file, WAL, foreign keys)
CLI / MCP ──┘            │
                         ▼
                       Mailgun API  (delivery, tracking, deliverability)
```

A single binary (Go) or a Cloudflare Worker. Same HTTP API, same database schema, same HMAC token format — tokens issued by one verify on the other. Bulk sends are always async: `POST /send/bulk` returns a `send_id` immediately while a background worker (goroutine in the Go server, self-invoking HTTP chain in the Worker) drives the loop.

## Install

Three flavors, pick one. They all run the same server.

| Target | Walkthrough |
|---|---|
| **Docker / Docker Compose** | [docs/docker.md](./docs/docker.md) |
| **Go binary (systemd / bare-metal)** | [docs/binary.md](./docs/binary.md) |
| **Cloudflare Worker + D1** | [docs/cloudflare.md](./docs/cloudflare.md) |

Then install the CLI on your laptop:

```bash
go install github.com/ranaroussi/minigun/cli@latest
```

The CLI also doubles as an MCP server for Claude Desktop, Cursor, Zed, and other AI clients. See [docs/cli.md](./docs/cli.md) for the full command + MCP reference.

## API

The server speaks JSON over HTTP on `:8080`. When `MINIGUN_API_TOKEN` is set, all routes require `Authorization: Bearer <token>` except `/healthz`, `/u/{token}`, and `/manage/{token}` (the last two carry their own HMAC token in the URL).

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
| POST   | `/lists/{list}/unsubscribe`                | Admin unsubscribe by email. |
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

`{list}` and `{company}` accept either id or slug. Listing endpoints use opaque base64 cursors; default `limit` is 50, max 500.

## Configuration

| Env var                        | Required | Default                  | Purpose |
|--------------------------------|----------|--------------------------|---------|
| `MAILGUN_API_KEY`              | yes      | —                        | Mailgun API key (HTTP Basic password; user is `api`). |
| `MAILGUN_DOMAIN`               | no       | derived from each send's `from` | Sending domain. If unset, MiniGun parses the host part of each send's `from` header. |
| `MAILGUN_REGION`               | no       | `us`                     | `us` or `eu`. |
| `MAILGUN_API_BASE`             | no       | derived from region      | Explicit override for the API base URL. |
| `MINIGUN_PUBLIC_URL`           | yes      | —                        | Public origin used to build per-recipient unsubscribe URLs. |
| `MINIGUN_HMAC_SECRET`          | yes      | —                        | Secret used to HMAC-sign unsubscribe / manage tokens. |
| `MINIGUN_API_TOKEN`            | no       | —                        | Bearer token required on every API request when set. |
| `MINIGUN_DB_PATH`              | no       | `/data/minigun.db`       | SQLite file path. |
| `MINIGUN_LISTEN_ADDR`          | no       | `:8080`                  | HTTP listen address. |
| `MINIGUN_TURNSTILE_SITE_KEY`   | no       | —                        | Cloudflare Turnstile site key. |
| `MINIGUN_TURNSTILE_SECRET_KEY` | no       | —                        | Turnstile secret. Required when site key is set. |

Per-deployment specifics (D1 binding for the Worker, secrets vs vars, etc.) live in the install docs.

## Development

```bash
cd src
go build ./...      # build everything
go test ./...       # run unit tests
go vet ./...        # static analysis
go run . serve      # run the server with env from your shell
```

The CLI lives in its own Go module:

```bash
cd cli
go build ./...
go test ./...
```

The Worker:

```bash
cd worker
npm install
npx tsc --noEmit
npx wrangler dev
```

## Project layout

```
.
├── README.md
├── docs/                   # install + CLI + MCP walkthroughs
│   ├── binary.md
│   ├── docker.md
│   ├── cloudflare.md
│   └── cli.md
├── Dockerfile              # multi-stage Alpine build for the Go server
├── docker-compose.yml      # example service-style deployment
├── src/                    # the Go server (module: github.com/ranaroussi/minigun)
│   ├── main.go
│   ├── cmd/
│   └── internal/
│       ├── api/            # chi handlers
│       ├── config/         # env loader
│       ├── db/             # SQLite + embedded goose migrations
│       ├── ids/            # prefixed random IDs
│       ├── mailgun/        # Mailgun client (messages + Metrics API)
│       ├── models/         # domain types
│       ├── render/         # markdown → HTML/text, variable rewriter
│       ├── store/          # SQLite repository layer
│       ├── tmpl/           # embedded unsubscribe.html / manage.html
│       ├── token/          # HMAC unsubscribe tokens
│       ├── turnstile/      # Cloudflare Turnstile siteverify
│       └── worker/         # bulk send worker + stats refresher
├── cli/                    # the standalone CLI (module: github.com/ranaroussi/minigun/cli)
│   ├── main.go
│   └── cmd/
└── worker/                 # Cloudflare Worker port (TypeScript + Hono + D1 + Web Crypto)
    ├── wrangler.toml
    ├── migrations/         # D1 migrations mirroring the Go server's goose migrations
    └── src/                # routes, store, send pipeline, pages, lib
```
