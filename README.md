# MiniGun

A tiny, self-hosted email sender on top of [Mailgun](https://mailgun.com). SQLite for state, single Go binary, no Redis, no Postgres, no JavaScript build step.

MiniGun handles the parts of bulk email that you usually end up gluing together yourself: contacts, lists, subscriptions, batched sends with crash-safe resume, per-recipient unsubscribe links that you actually own (no Mailgun suppression-list lock-in), and a CAPTCHA-protected two-step unsubscribe page so email scanners don't silently nuke your recipients.

```bash
# spin up the server (Docker)
docker compose up -d

# from your laptop, install the CLI
go install github.com/ranaroussi/minigun/cli@latest

# create a list, add a contact, send
minigun list create --name "Weekly Newsletter" --slug newsletter
minigun contact add newsletter ran@example.com --params '{"first_name":"Ran"}'
minigun send bulk \
  --list newsletter \
  --subject "Weekly update" \
  --from "Ran <ran@example.com>" \
  --md ./email.md
```

See [PRD.md](./PRD.md) for the full design rationale.

## Features

- **Contacts + lists + subscriptions** stored in SQLite (one file, WAL mode, foreign keys, busy-timeout, no CGO).
- **Bulk sends with resume.** Sends are checkpointed by `subscriptions.id`; if the worker crashes you simply resume. Recipients added after a send starts are not pulled into the in-flight send.
- **Single sends** for transactional email through the same Mailgun pipeline.
- **MiniGun owns unsubscribes.** Stateless HMAC tokens (no DB lookup to verify), RFC 2369 + RFC 8058 headers, two-step confirmation + optional Cloudflare Turnstile so security scanners and link prefetchers don't silently unsubscribe people.
- **Mailgun Metrics API** for per-send aggregate stats (legacy Stats / Events APIs are deprecated and not used).
- **`/healthz` endpoint** for container probes.
- **US and EU regions** via `MAILGUN_REGION`.
- **Single static binary**, no CGO, no runtime dependencies.
- **Companies / brands** group related lists under one parent so a recipient on the *Acme Newsletter* and the *Acme Product Updates* lists sees a single combined preferences page on `/manage/{token}`, not two separate ones.
- **Auto-injected unsubscribe footer.** If your email content has neither `{{unsubscribe}}` nor `{{unsub_url}}`, MiniGun appends a compliant footer (`<p>&nbsp;<br><a href="{{unsubscribe}}">Unsubscribe</a></p>` for HTML, plain text equivalent for text). The `List-Unsubscribe` + `List-Unsubscribe-Post: List-Unsubscribe=One-Click` headers (RFC 8058) are always set on bulk sends.
- **Cloudflare Worker port** in [`worker/`](./worker) for serverless deploys onto D1 + Workers, with full API parity.

## Quick start

### Docker Compose (recommended)

```yaml
services:
  minigun:
    image: ghcr.io/ranaroussi/minigun:latest
    restart: unless-stopped
    ports:
      - "8080:8080"
    volumes:
      - ./data:/data
    environment:
      MAILGUN_API_KEY: ${MAILGUN_API_KEY}
      MAILGUN_DOMAIN: ${MAILGUN_DOMAIN}
      MAILGUN_REGION: us
      MINIGUN_PUBLIC_URL: https://minigun.example.com
      MINIGUN_HMAC_SECRET: ${MINIGUN_HMAC_SECRET}
      MINIGUN_TURNSTILE_SITE_KEY: ${MINIGUN_TURNSTILE_SITE_KEY:-}
      MINIGUN_TURNSTILE_SECRET_KEY: ${MINIGUN_TURNSTILE_SECRET_KEY:-}
```

```bash
export MINIGUN_HMAC_SECRET=$(openssl rand -hex 32)
docker compose up -d
```

A starter `docker-compose.yml` is included at the repo root.

### Docker (plain)

```bash
docker run -d --name minigun --restart unless-stopped \
  -p 8080:8080 \
  -v "$PWD/data:/data" \
  -e MAILGUN_API_KEY=key-... \
  -e MAILGUN_DOMAIN=mg.example.com \
  -e MAILGUN_REGION=us \
  -e MINIGUN_PUBLIC_URL=https://minigun.example.com \
  -e MINIGUN_HMAC_SECRET="$(openssl rand -hex 32)" \
  ghcr.io/ranaroussi/minigun:latest
```

### From source

```bash
cd src
go build -o minigun .

MAILGUN_API_KEY=... \
MAILGUN_DOMAIN=... \
MINIGUN_PUBLIC_URL=http://localhost:8080 \
MINIGUN_HMAC_SECRET="$(openssl rand -hex 32)" \
MINIGUN_DB_PATH=./minigun.db \
./minigun serve
```

## Configuration

All configuration is via environment variables.

| Env var                        | Required | Default                  | Purpose |
|--------------------------------|----------|--------------------------|---------|
| `MAILGUN_API_KEY`              | yes      | —                        | Mailgun API key. Sent as HTTP Basic password (user is `api`). |
| `MAILGUN_DOMAIN`               | no       | derived from `from`      | Sending domain. Used in `POST /v3/<domain>/messages`. If unset, MiniGun parses the host part of each send's `from` header and uses that as the Mailgun domain. Set this only to override (e.g., displayed From on `acme.com` but sending through `mg.acme.com`). |
| `MAILGUN_REGION`               | no       | `us`                     | `us` or `eu`. Selects `https://api.mailgun.net` vs `https://api.eu.mailgun.net`. |
| `MAILGUN_API_BASE`             | no       | derived from region      | Explicit override for the API base URL. |
| `MINIGUN_PUBLIC_URL`           | yes      | —                        | Public origin used to build per-recipient unsubscribe URLs. |
| `MINIGUN_HMAC_SECRET`          | yes      | —                        | Secret used to HMAC-sign unsubscribe tokens. Treat as sensitive. |
| `MINIGUN_DB_PATH`              | no       | `/data/minigun.db`       | SQLite file path. |
| `MINIGUN_LISTEN_ADDR`          | no       | `:8080`                  | HTTP listen address. |
| `MINIGUN_TURNSTILE_SITE_KEY`   | no       | —                        | Cloudflare Turnstile site key. If unset, the two-step confirmation page is still shown but no bot challenge is rendered. |
| `MINIGUN_TURNSTILE_SECRET_KEY` | no       | —                        | Turnstile secret. Required when site key is set. |
| `MINIGUN_API_TOKEN`            | no       | —                        | Bearer token required on every request when set. The `/healthz`, `/u/{token}` and `/manage/{token}` routes stay public. If unset, the server runs open and logs a warning at startup. |

### API authentication

When `MINIGUN_API_TOKEN` is set, the server enforces `Authorization: Bearer <MINIGUN_API_TOKEN>` on all routes except:

- `GET /healthz` — orchestration probes
- `GET|POST /u/{token}` — public per-recipient unsubscribe page (already scoped by HMAC token)
- `GET|POST /manage/{token}` — public per-recipient preferences page (HMAC token)

When `MINIGUN_API_TOKEN` is empty, the server runs without auth and prints a warning at startup. Set the token in production. The CLI (`minigun --token` / `MINIGUN_API_TOKEN`) forwards it automatically.

```bash
export MINIGUN_API_TOKEN="$(openssl rand -hex 32)"
```

## API

The server speaks JSON over HTTP. See [PRD.md](./PRD.md#api) for full request/response shapes.

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
| GET    | `/send/{id}/stats`                         | Aggregate stats (Mailgun Metrics + local unsubscribes). |
| GET    | `/u/{token}`                               | Render the unsubscribe confirmation page. |
| POST   | `/u/{token}`                               | Perform the unsubscribe (form post or RFC 8058 one-click). |
| GET    | `/manage/{token}`                          | Render the combined company-wide preferences page. |
| POST   | `/manage/{token}`                          | Apply preference deltas across the company's lists. |

`{list}` and `{company}` accept either id or slug. Listing endpoints use opaque base64 cursors; default `limit` is 50, max 500.

## CLI

A standalone CLI lives in [`cli/`](./cli) and ships as a separate, slim binary you can install on your laptop to drive a remote MiniGun server.

```bash
# install
go install github.com/ranaroussi/minigun/cli@latest

# point it at your server
export MINIGUN_API_URL=https://minigun.example.com
export MINIGUN_API_TOKEN=...   # optional, future-proofing

minigun health
minigun list create --name "Weekly" --slug newsletter
minigun contact add newsletter ran@example.com --params '{"first_name":"Ran"}'
minigun send bulk --list newsletter --subject "Hi" --from "Ran <r@x.com>" --md ./email.md
minigun send status s_8Kx29aPqz --watch
```

See [`cli/README.md`](./cli/README.md) for the full command reference.

## MCP (AI clients)

The same `minigun` binary doubles as a [Model Context Protocol](https://modelcontextprotocol.io) server over stdio. Point an MCP-aware AI client (Claude Desktop, Cursor, Zed, Continue, Goose, etc.) at `minigun mcp` and it can list, create, send, and audit through natural language.

```json
{
  "mcpServers": {
    "minigun": {
      "command": "minigun",
      "args": ["mcp"],
      "env": {
        "MINIGUN_API_URL": "https://minigun.example.com",
        "MINIGUN_API_TOKEN": "..."
      }
    }
  }
}
```

See [`mcp-prd.md`](./mcp-prd.md) for the design and [`cli/README.md`](./cli/README.md#minigun-mcp) for the tool / resource / prompt inventory.

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

## Project layout

```
.
├── PRD.md             # product / design spec
├── README.md          # you are here
├── Dockerfile         # multi-stage Alpine build for the server
├── docker-compose.yml # example service-style deployment
├── src/               # the server (Go module: github.com/ranaroussi/minigun)
│   ├── main.go
│   ├── cmd/           # `minigun serve` + in-binary admin commands
│   └── internal/
│       ├── api/       # chi handlers
│       ├── config/    # env loader
│       ├── db/        # SQLite open + embedded goose migrations
│       ├── ids/       # prefixed random IDs
│       ├── mailgun/   # Mailgun client (messages + Metrics API)
│       ├── models/    # domain types and enums
│       ├── render/    # markdown → HTML/text, {{var | "default"}} rewriter
│       ├── store/     # SQLite repository layer
│       ├── tmpl/      # embedded unsubscribe.html
│       ├── token/     # HMAC unsubscribe tokens
│       ├── turnstile/ # Cloudflare Turnstile siteverify
│       └── worker/    # bulk send worker (per-send goroutine)
├── cli/               # the standalone CLI (Go module: github.com/ranaroussi/minigun/cli)
│   ├── main.go
│   └── cmd/
└── worker/            # Cloudflare Worker port (TypeScript + Hono + D1 + Web Crypto)
    ├── README.md      # deploy/operate this server
    ├── wrangler.toml
    ├── migrations/    # D1 migrations mirroring the Go server's goose migrations
    └── src/           # routes, store, send pipeline, pages, lib
```

## Cloudflare Worker (alternate deploy)

A second implementation of the same server lives in [`worker/`](./worker) — same API, same database schema, same HMAC token format. It runs on Cloudflare Workers + D1 instead of a Go process + SQLite, which is useful if you want to host the whole thing inside Cloudflare's edge.

Key differences from the Go server:

- **Bulk sends use HTTP self-call resume** instead of an in-process worker goroutine. Each batch step fires the next via `POST /send/{id}/next`; a 1-minute cron sweeps for any send stuck in `running` for > 2 minutes and nudges it back into the loop.
- **D1 migrations** mirror the Go server's goose migrations (`worker/migrations/0001_init.sql`, `0002_companies.sql`).
- **Web Crypto** for HMAC. Token wire format is bit-identical with the Go server, so tokens issued by one verify on the other.

See [`worker/README.md`](./worker/README.md) for setup, deploy, and operational notes.
