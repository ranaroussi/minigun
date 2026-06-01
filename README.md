<img width="1720" height="688" alt="image" src="https://github.com/user-attachments/assets/039d9c6d-7488-49ac-a791-621f36122c95" />

# MiniGun

> A tiny self-hosted email layer on top of [Mailgun](https://www.mailgun.com). Write in **Markdown**, drive it from a **CLI** or any **AI client over MCP**, deploy it to **Cloudflare's edge** — zero infra, no Redis, no queue, no long-running process.

[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](./LICENSE)
[![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white)](./src)
[![Cloudflare Workers](https://img.shields.io/badge/Cloudflare-Workers%20%2B%20D1-F38020?logo=cloudflare&logoColor=white)](./docs/cloudflare.md)
[![MCP](https://img.shields.io/badge/MCP-ready-111?logo=anthropic&logoColor=white)](./docs/cli.md#mcp-server-minigun-mcp)
[![SDKs](https://img.shields.io/badge/SDKs-PHP%20·%20Python%20·%20TS%20·%20Go-success)](./sdks)

Also packaged as a single Go binary or a Docker container if you'd rather host it yourself.

**Mailgun does delivery, tracking, and deliverability. MiniGun owns everything else you'd otherwise glue together** — contacts, lists, unsubscribe pages, scheduling, crash recovery, stats persistence, and list cleanup.

**Jump to:** [Quickstart](#quickstart) · [CLI](#a-first-class-cli) · [Deploy to Cloudflare](./docs/cloudflare.md) · [Drive it with an AI](#mcp-server--drive-it-with-an-ai) · [SDKs](#sdks) · [API](#api)

---

## Contents

- [Features](#features)
- [Quickstart](#quickstart)
- [Install](#install)
- [Your first newsletter](#your-first-newsletter)
- [Why MiniGun](#why-minigun)
- [What makes MiniGun different](#what-makes-minigun-different)
- [Architecture](#architecture)
- [SDKs](#sdks)
- [Agent skill](#agent-skill)
- [API](#api)
- [Configuration](#configuration)
- [Development](#development)
- [Project layout](#project-layout)
- [License](#license)

## Features

| Feature | What you get | Deep dive |
|---|---|---|
| **Markdown authoring** | Write emails in Markdown with `{{first_name \| "there"}}` variable defaults. Optional `---` frontmatter sets `subject` / `from` / `preheader` / `reply_to` right in the file. Rendered to HTML + text, no MJML, no HTML builder, no second template language. | [below](#markdown-first-authoring) |
| **Crash-safe bulk sends** | 100k-recipient sends that survive worker restarts and resume from the last completed batch — on a Cloudflare Worker with no long-running process. | [below](#bulk-email-on-the-cloudflare-edge) |
| **Scheduled sends** | `--send-at <RFC3339>` parks a bulk or single send until a background dispatcher fires it. No Mailgun 3-day cap; `send cancel` unschedules it. Bulk audience resolves at dispatch, so late signups are included. | [docs/cli.md](./docs/cli.md#minigun-send-bulk) |
| **Automatic list hygiene** | Hard bounces and spam complaints purge themselves in real time via a signed Mailgun webhook; an engagement-based prune unsubscribes dormant contacts on three configurable signals. Your list self-heals. | [docs/list-hygiene.md](./docs/list-hygiene.md) |
| **Engagement rollups & segmentation** | Per-`(send, contact)` and per-`(contact, list)` rollups + a per-URL click tier that outlive Mailgun's 5-day retention. Segment by who opened/clicked what; powers prune-by-engagement. | [docs/events-archive.md](./docs/events-archive.md) |
| **HMAC unsubscribe tokens** | Stateless tokens — no DB lookup to verify, no Mailgun suppression-list lock-in. You own the unsub flow forever. | [below](#what-makes-minigun-different) |
| **Edge-native** | Same HTTP API, schema, and token format as the Go server, but as a Cloudflare Worker on D1. No process to crash. | [docs/cloudflare.md](./docs/cloudflare.md) |
| **First-class CLI** | One `go install`, every server operation behind sensible flags, `--watch` mode for tailing in-flight sends. | [docs/cli.md](./docs/cli.md) |
| **Agent-ready** | MCP server + a deep operator [skill](./skill/minigun/SKILL.md): dispatch rules, IP warming, DMARC graduation, anti-patterns to push back on. | [below](#mcp-server--drive-it-with-an-ai) |
| **Zero-dep SDKs** | Single drop-in files for PHP / Python / TypeScript / Go — no `axios` / `requests` / `guzzle`. | [sdks/](./sdks) |

## Quickstart

Install the CLI:

```bash
go install github.com/ranaroussi/minigun/cli/cmd/minigun@latest
```

If you already have a MiniGun deployment (or are running one locally via `wrangler dev`), verify it end-to-end with Mailgun's `testmode` — the message is accepted, logged, and counted, but **never delivered**:

```bash
export MINIGUN_API_URL=https://mailer.example.com
export MINIGUN_API_TOKEN=...

minigun health                                            # is the worker up?
minigun send single --testmode \
  --to you@example.com \
  --from "You <you@example.com>" \
  --subject "MiniGun smoke test" \
  --company acme \
  --md "Hi {{first_name | 'there'}}, this is a test."
```

Confirm the send appears in your Mailgun dashboard (Sending → Logs) and you've verified every layer — DNS, auth, the worker, Mailgun — without spamming yourself.

## Install

Don't have a MiniGun deployment yet? Pick one of three install paths — every one is documented:

| Target | When to pick it | Walkthrough |
|---|---|---|
| **Cloudflare Worker + D1** | Zero infra. Free for any volume Mailgun's free tier supports. Bulk sends survive crashes because there's no process to crash. | [docs/cloudflare.md](./docs/cloudflare.md) |
| **Go binary** | On-prem, single VM, or you want a long-running process you can `systemctl` around. | [docs/binary.md](./docs/binary.md) |
| **Docker / Compose** | Containerised stack; one-line `docker run` and you're up. | [docs/docker.md](./docs/docker.md) |

## Your first newsletter

The full arc — create a list, add a subscriber, write copy in Markdown, send to the list:

```bash
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

The `POST /send/bulk` returns a `send_id` immediately and a background loop drives the batches. Poll status with `minigun send status <id> --watch` and stats with `minigun send stats <id>` — persisted locally forever, even after Mailgun's 5-day event log retention expires. Add `--send-at 2026-06-01T09:00:00Z` to schedule it instead.

## Why MiniGun

Mailgun is excellent at sending email. It is not opinionated about how you store contacts, how recipients unsubscribe, or how you resume a bulk send after a crash — those are your problem. MiniGun is the thin layer that solves them and stays out of the way.

**Mailgun keeps owning what it's good at:** delivery, open/click tracking, deliverability, bounce handling, reputation, IP warmup.

**MiniGun owns the parts you'd otherwise glue together by hand:**

- **Contacts and lists**, in SQLite (or D1), with arbitrary per-contact JSON metadata.
- **Unsubscribe links you actually own** — stateless HMAC tokens, no DB lookup to verify, no Mailgun suppression-list lock-in. Backed by a two-step CAPTCHA-protected unsubscribe page so email security scanners (Microsoft Defender, Gmail link inspection, Apple Mail Privacy Protection) don't silently unsubscribe recipients by pre-fetching the link. RFC 2369 + RFC 8058 one-click headers on every bulk send (required by Gmail/Yahoo bulk-sender rules).
- **Crash-safe bulk sends.** Each batch is checkpointed by a monotonic subscription id; if the process dies mid-send you resume from where you left off. Recipients added during a send don't get pulled in mid-flight.
- **Scheduled sends.** `--send-at <RFC3339>` (bulk or single) parks a send in a `scheduled` status until a background dispatcher fires it — no Mailgun-side deferral, so no 3-day cap, and `minigun send cancel <id>` unschedules it with a single guarded status flip. A scheduled *bulk* send resolves its recipient set when it fires, so contacts who subscribe before then are included (additions during the send itself are still excluded).
- **Companies / brands** group related lists so a recipient on the *Acme Newsletter* and *Acme Product Updates* lists sees a single combined preferences page.
- **Permanent per-send stats.** Mailgun retains event logs for only 5 days; MiniGun pulls the Metrics API on a front-loaded schedule (+0, +1h, +6h, +24h, +48h, +5d after completion) and persists the aggregates locally.
- **Clean Gmail rendering** on cross-domain `From` headers. MiniGun always sets `Sender: <From>` so Mailgun doesn't rewrite it to a VERP bounce address (which is what makes Gmail show `via mailgun-route.example.com` and hide the native one-click unsubscribe).
- **Dry-run sends.** `--testmode` runs the full pipeline through to Mailgun with `o:testmode=yes`: accepted and logged but not delivered. Persisted on the send row, so cron-resumed chains keep it set.
- **Automatic list hygiene.** A Mailgun-signed webhook auto-removes hard-bouncing addresses and spam-complainers in real time, plus an engagement-based prune for the dormant middle ground. See [docs/list-hygiene.md](./docs/list-hygiene.md).

The philosophy:

> Keep sending simple. Let Mailgun do the heavy lifting. Own your contacts and unsubscribe flow.

## What makes MiniGun different

### Bulk email on the Cloudflare edge

The same HTTP API, the same database schema, the same HMAC token format as the Go server — but as a Cloudflare Worker backed by D1. **No long-running process.** The bulk-send loop is a chain of self-invoking HTTP requests guarded by an atomic D1 batch claim: each step processes one batch and `ctx.waitUntil(fetch(/next))`s the next. A once-a-minute cron sweeps any send whose chain has gone quiet (and dispatches due scheduled sends), so even a worker crash recovers without your involvement. The same cron refreshes per-send stats on a front-loaded schedule so historical sends keep their numbers forever.

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
minigun send    bulk       --list weekly --subject "Hi" --from "Ran <r@x.com>" --md ./email.md --send-at 2026-06-01T09:00:00Z
minigun send    cancel     s_xxxx          # unschedule a scheduled/queued send
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

Destructive tools (`send_bulk`, `send_single`, `unsubscribe_contact`, `resume_send`, `cancel_send`) are tagged so the client renders an explicit confirmation prompt. Two built-in **prompts** (`compose_newsletter`, `audit_send`) encode the two most common operator workflows.

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

Single newlines are honored as line breaks (so a list of short lines renders one per line); separate paragraphs with a blank line.

**Frontmatter.** Start the Markdown with a `---` fenced block (three or more dashes) and the CLI, MCP tools, and all four SDKs read the per-send headers straight from the file — so `--subject` / `--from` / `--preheader` / `--reply-to` become optional:

```markdown
---
subject: "Tuesday digest — what's new"
preheader: A quick look at this week's releases
from: Ran <ran@example.com>
reply_to: support@example.com
---

Hi {{first_name | "there"}}, ...
```

```bash
minigun send bulk --list weekly --md ./week-12.md   # subject/from/preheader all come from the file
```

An explicit flag/argument always wins; the block is stripped from the body so it never renders into the email; unknown keys are ignored. It's a client-side authoring convenience — the HTTP API still takes these as explicit fields. See [docs/cli.md](./docs/cli.md#markdown-frontmatter).

### Automatic list hygiene

The single biggest reason newsletter senders trash their reputation is mailing addresses that no longer exist (hard bounces), actively flagged the previous send as spam, or keep receiving messages they never engage with. MiniGun handles all three automatically:

- **Reactive** — a Mailgun-signed webhook (`POST /webhooks/mailgun`) auto-purges hard bounces and spam complaints in real time. HMAC-verified, replay-bounded, idempotent against Mailgun's retries. Fails closed (401) when no signing key is configured.
- **Proactive** — `POST /lists/{list}/prune` unsubscribes the dormant middle ground (addresses that exist and don't complain but never engage) on three OR'd, `dry_run`-by-default signals, with an opt-in daily auto-prune cron.

→ Full reference, setup, and the hard-purge / prune command surfaces: **[docs/list-hygiene.md](./docs/list-hygiene.md)**

### Engagement rollups & segmentation

With `EVENTS_ARCHIVE_ENABLED=true`, MiniGun pulls Mailgun's events API on a burst-then-daily schedule for 30 days and folds each event into bounded rollups — **no raw per-event log**:

- **`contact_message_engagement`** — per-`(send, contact)`: sent/delivered, first/last open + click with counts, failure/complaint/unsubscribe state.
- **`contact_engagement`** — per-`(contact, list)` lifetime summary that powers prune-by-engagement.
- **`contact_message_clicks`** — per-`(send, contact, url)` click rollup with canonicalized URLs, for "who clicked this link" segmentation.

These outlive Mailgun's 5-day retention and are exposed over HTTP, MCP, and the SDKs (`minigun send recipients`, `minigun send clicks`, `minigun contact engagement`). Opt-in: the schema ships dormant until you flip the flag.

→ Tiers, read endpoints, and operational guarantees: **[docs/events-archive.md](./docs/events-archive.md)**

## Architecture

```
your app  ──┐
            ├──►  MiniGun API  ──►  SQLite or D1
CLI / MCP ──┘            │
                         ▼
                       Mailgun API  (delivery, tracking, deliverability)
```

One HTTP service, two implementations (Go binary, Cloudflare Worker), one shared schema, one shared token format. Bulk sends are always async: `POST /send/bulk` returns a `send_id` immediately while a background loop (goroutine in the Go server, self-invoking HTTP chain in the Worker) drives the batches forward. Scheduled sends are parked until a dispatcher (a 30s ticker in the Go server, the per-minute cron in the Worker) fires them through the same path.

## SDKs

Single-file, zero-dependency drop-in clients for the most common server languages. Every SDK exposes the same surface (contacts, sends, scheduling/cancel, status, stats, resume, delete) with the same error model — pick whichever fits your stack:

| Language | File | Drop in / install | Reference |
|---|---|---|---|
| **PHP** (7.4+) | [`sdks/php/minigun.php`](./sdks/php/minigun.php) | `require_once 'minigun.php';` | [sdks/php/README.md](./sdks/php/README.md) |
| **Python** (3.9+) | [`sdks/python/minigun.py`](./sdks/python/minigun.py) | drop the file in, `from minigun import Minigun` | [sdks/python/README.md](./sdks/python/README.md) |
| **TypeScript** (ES2020+) | [`sdks/typescript/minigun.ts`](./sdks/typescript/minigun.ts) | drop the file in, `import { Minigun } from './minigun'` | [sdks/typescript/README.md](./sdks/typescript/README.md) |
| **Go** (1.21+) | [`sdks/go/minigun.go`](./sdks/go/minigun.go) | `go get github.com/ranaroussi/minigun/sdks/go` | [sdks/go/README.md](./sdks/go/README.md) |

All four are stdlib-only — no `requests`, no `axios`, no `guzzle`, no external Go modules — so they slot into any project without touching its dependency tree. The TypeScript SDK uses the standard `fetch` API, so it runs unchanged on Node 18+, Bun, Deno, Cloudflare Workers, and the browser.

See [sdks/README.md](./sdks/README.md) for the cross-language overview and the matching method-name table.

## Agent skill

For [Factory Droid](https://factory.ai) (and any AI client that loads external context files), this repo ships a complete operator skill at [`skill/minigun/SKILL.md`](./skill/minigun/SKILL.md). It's not a CLI wrapper — it's a deep playbook covering: how to pick `send_single` vs `send_bulk`, the pre-send checklist, post-send polling, failure recovery, the IP warming schedule, DMARC graduation, content red flags, list hygiene, and the anti-patterns to push back on.

Install once with a symlink to keep it in sync as the repo evolves:

```bash
mkdir -p ~/.factory/skills
ln -s "$(pwd)/skill/minigun" ~/.factory/skills/minigun
```

Pair it with the MiniGun MCP server (covered above under *MCP server — drive it with an AI*) for full autonomy — the skill teaches the playbook, the MCP server gives the agent hands.

→ [skill/README.md](./skill/README.md) for install / usage from other AI clients.

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
| POST   | `/send/bulk`                               | Start a bulk send. Optional `send_at` (RFC3339) schedules it for later. |
| POST   | `/send/single`                             | Send a single transactional email. Optional `send_at` (RFC3339) schedules it for later. |
| POST   | `/send/{id}/next`                          | Execute the next batch step (chain self-call; alias of `/resume`). |
| POST   | `/send/{id}/resume`                        | Resume a paused / failed send (alias of `/next`). |
| POST   | `/send/{id}/cancel`                        | Unschedule a `scheduled`/`queued` send (→ `cancelled`). `409` once running or terminal. |
| GET    | `/send/{id}`                               | Send status + progress. |
| GET    | `/send/{id}/stats`                         | Aggregate stats (DB-backed; falls back to live Mailgun for fresh sends). |
| GET    | `/send/{id}/recipients?limit=&cursor=` | Per-recipient message engagement rollup for a send (one row per contact; keyset-paginated by `contact_id`). Requires `EVENTS_ARCHIVE_ENABLED=true`. |
| GET    | `/send/{id}/clicks?limit=&cursor=`         | Per-URL click rollup for a send (one row per contact + clicked link). Requires `EVENTS_ARCHIVE_ENABLED=true`. |
| GET    | `/contacts/{idOrEmail}/engagement?list_id=` | Contact's per-list lifetime engagement summary (totals + last open/click + dormancy counter). Requires `EVENTS_ARCHIVE_ENABLED=true`. |
| POST   | `/lists/{list}/prune`                      | Engagement-based prune. Body accepts `min_messages_since_engagement`, `dormant_for_days`, `no_delivery_for_days`, `dry_run` (default true), `limit`, `sample_size`. |
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
| `EVENTS_ARCHIVE_ENABLED`       | no       | `false`                  | Activates the Mailgun events archive pull cron + the read surface (`/send/{id}/recipients`, `/send/{id}/clicks`, `/contacts/{id}/engagement`). Schema and send-path tagging ship dormant; flip to `true` whenever you're ready to start collecting. See [docs/events-archive.md](./docs/events-archive.md). |
| `LIST_HYGIENE_AUTO_PRUNE_ENABLED` | no    | `false`                  | When `true`, the engagement-based prune executor runs once per day against every list. Manual `POST /lists/{list}/prune` works independently. See [docs/list-hygiene.md](./docs/list-hygiene.md). |
| `LIST_HYGIENE_AUTO_PRUNE_BY_COUNT` | no   | `20`                     | Auto-prune contacts whose `messages_since_last_engagement >= N`. Set to `0` to disable this criterion in the cron. |
| `LIST_HYGIENE_AUTO_PRUNE_BY_RECENCY_DAYS` | no | `180`              | Auto-prune contacts whose last open/click is older than N days. Set to `0` to disable. |
| `LIST_HYGIENE_AUTO_PRUNE_NO_DELIVERY_DAYS` | no | `0` (disabled)    | Auto-prune contacts subscribed before the cutoff with no delivered events in N days. Aggressive on new lists — defaults disabled. |

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
├── docs/                   # install + CLI + MCP + hygiene + archive walkthroughs
│   ├── cloudflare.md
│   ├── cli.md
│   ├── binary.md
│   ├── docker.md
│   ├── list-hygiene.md
│   └── events-archive.md
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
│       └── worker/         # bulk send worker + scheduler + stats refresher
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
├── sdks/                   # Single-file, zero-dep client SDKs (one per language)
│   ├── php/minigun.php
│   ├── python/minigun.py
│   ├── typescript/minigun.ts
│   └── go/{go.mod,minigun.go}
└── skill/                  # Agent (and any MCP-aware agent) operator skill
    └── minigun/SKILL.md
```

## License

[MIT](./LICENSE) © 2026 Ran Aroussi
