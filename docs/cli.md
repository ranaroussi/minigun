# CLI and MCP

The `minigun` binary doubles as a human CLI and as an [MCP](https://modelcontextprotocol.io) server over stdio. Install it on your laptop and point it at a remote MiniGun server.

The CLI is a separate, slim Go module in [`cli/`](../cli/) so you don't pull the server's SQLite, Mailgun, goose, and goldmark dependencies just to install a client.

## Install

```bash
go install github.com/ranaroussi/minigun/cli/cmd/minigun@latest
```

`go install` names the binary after the last path component, so this lands as `minigun` in `$(go env GOBIN)` (or `$(go env GOPATH)/bin` if `GOBIN` is unset). Make sure that directory is on your `$PATH`.

Or build from a local checkout:

```bash
cd cli
go build -o minigun ./cmd/minigun
mv minigun /usr/local/bin/        # or anywhere on PATH
```

If you also run the server binary on the same machine, alias one of them.

## Configuration

| Flag        | Env var              | Default                  |
|-------------|----------------------|--------------------------|
| `--api`     | `MINIGUN_API_URL`    | `http://127.0.0.1:8080`  |
| `--token`   | `MINIGUN_API_TOKEN`  | _(unset)_                |

`MINIGUN_API_TOKEN`, if set, is sent as `Authorization: Bearer <token>` on every request. The server enforces this header on all endpoints when `MINIGUN_API_TOKEN` is set on the server side. `/healthz`, `/u/{token}`, and `/manage/{token}` stay public.

```bash
export MINIGUN_API_URL=https://minigun.example.com
export MINIGUN_API_TOKEN=...
```

## Commands

### `minigun health`

Probe the server's `/healthz` endpoint.

```bash
minigun health
```

### `minigun company create`

Every list is owned by a company, and a company carries the Mailgun sending domain that its lists inherit by default.

```bash
minigun company create \
  --name "Acme Co" \
  --slug acme \
  --domain mail.acme.com
```

`--domain` is required: it's the Mailgun-verified sending domain MiniGun will use when posting to the Mailgun API for any send under this company.

### `minigun list create`

```bash
minigun list create \
  --name "Weekly Newsletter" \
  --slug newsletter \
  --company acme
```

The list inherits its `sending_domain` from `--company` at creation time. If you operate one company across multiple Mailgun domains, pass an explicit `--domain` to override the inherited value for this list only:

```bash
minigun list create \
  --name "Operational Alerts" \
  --slug ops \
  --company acme \
  --domain alerts.acme.com
```

### `minigun contact add <list> <email>`

```bash
minigun contact add newsletter ran@example.com --params '{"first_name":"Ran"}'
```

`<list>` accepts either a list slug or list id. `--params` must be a JSON object.

### `minigun contact unsubscribe <list> <email>`

Admin-side unsubscribe (does not require an unsubscribe token).

```bash
minigun contact unsubscribe newsletter ran@example.com
```

### `minigun send bulk`

```bash
minigun send bulk \
  --list newsletter \
  --subject "Weekly update" \
  --from "Ran <ran@example.com>" \
  --reply-to "support@example.com" \
  --md ./email.md \
  --batch-size 500 \
  --throttle-ms 1000 \
  --notify "ran@example.com"
```

The Mailgun sending domain is read from `list.sending_domain` (set when you created the list). Pass `--domain mail.test.example.com` to override for this one send — the resolved value is persisted on the send row so every `/next` batch in the chain uses the same domain.

Optional unsubscribe-mode flags:

```bash
--unsub-mode redirect --unsub-redir https://example.com/goodbye
--unsub-mode external --unsub-url  https://example.com/unsubscribe
```

Add `--testmode` for a dry run. The API call is fully exercised (rendering, recipient resolution, tokens, Mailgun POST), Mailgun logs the message, and the send row tracks progress — but Mailgun does **not** actually deliver:

```bash
minigun send bulk --list newsletter --subject "Smoke test" \
  --from "Ran <ran@example.com>" --md ./email.md --testmode
```

The flag is persisted on the send row, so if the chain dies mid-send and the cron sweep resumes it, every subsequent batch stays in test mode.

**Scheduling.** Add `--send-at` with an RFC3339 timestamp to dispatch the send at a future time instead of now:

```bash
minigun send bulk --list newsletter --subject "Tuesday digest" \
  --from "Ran <ran@example.com>" --md ./email.md \
  --send-at 2026-06-01T09:00:00Z
```

The send is parked in the new `scheduled` status; a background dispatcher picks it up once `send_at` arrives (granularity is the dispatcher tick — sub-minute precision isn't guaranteed, which is fine for email). The recipient set is resolved when the send fires, so contacts who subscribe between scheduling and dispatch are included; additions *during* the send itself are still excluded (the same consistent-snapshot guarantee as an immediate send). A `send_at` in the past sends immediately. Unschedule a parked send with `minigun send cancel`.

### `minigun send single`

```bash
minigun send single \
  --to ran@example.com \
  --subject "Hello" \
  --from "Ran <ran@example.com>" \
  --company acme \
  --md ./hello.md
```

Single transactional sends don't belong to a list, so `--company` is required: MiniGun resolves the sending domain from `company.sending_domain`. Pass `--domain` to override. `--testmode` works here too, as does `--send-at` (same parking/dispatch/cancel mechanics as bulk; a single send targets one explicit recipient, so there's no audience to resolve — it sends to that address when it fires).

### Body and wrapper template

Both `send bulk` and `send single` build the message from the same set of body flags. You must supply **either `--md` or `--html`**; the rest are optional.

| Flag | What it does |
|------|--------------|
| `--md <file>` | Markdown body. Rendered to HTML **and** plain text, with `{{first_name \| "there"}}`-style placeholders rewritten into Mailgun recipient variables. |
| `--html <file>` | Pre-built HTML body, used when `--md` is omitted. MiniGun rewrites `{{var}}` placeholders and ensures an unsubscribe footer, but does **not** wrap it — you own the full document. |
| `--text <file>` | Optional plain-text part. Auto-derived from the Markdown / HTML when omitted. |
| `--preheader <text>` | Hidden inbox-preview snippet. |
| `--template <file>` | An HTML **wrapper** applied around the rendered Markdown body. |

The `--template` file is read by the CLI and sent to the server, which wraps the rendered Markdown into it. The wrapper is substituted with these placeholders (spaced forms like `{{ content }}` work too):

- `{{content}}` — **required**; replaced with the rendered Markdown body.
- `{{subject}}` / `{{preheader}}` — replaced with the send's subject / preheader.
- `{{unsubscribe}}` (or `{{unsub_url}}`) — optional. If the wrapper already contains an unsubscribe link, MiniGun skips its default auto-footer instead of double-injecting one.

The wrapper applies to the **Markdown path only** (`--md`). When you pass raw `--html`, you already control the whole document, so `--template` is ignored. Example:

```html
<!-- layout.html -->
<html>
  <head><title>{{subject}}</title></head>
  <body>
    <header><img src="https://example.com/logo.png" alt="Acme"></header>
    {{content}}
    <footer><a href="{{unsubscribe}}">Unsubscribe</a></footer>
  </body>
</html>
```

```bash
minigun send bulk --list newsletter --subject "Weekly update" \
  --from "Ran <ran@example.com>" --md ./week-12.md --template ./layout.html

minigun send single --to you@example.com --company acme --subject "Welcome" \
  --from "Ran <ran@example.com>" --md ./welcome.md --template ./layout.html
```

When `--template` is omitted, MiniGun uses a clean built-in default wrapper.

#### Markdown frontmatter

When the Markdown body starts with a YAML-style `---` fenced block, the CLI (and the MCP `send_bulk` / `send_single` tools) read these keys from it so the matching flags become optional:

| Frontmatter key | Fills flag |
|---|---|
| `subject` | `--subject` |
| `preheader` | `--preheader` |
| `from` | `--from` |
| `reply_to` (or `reply-to`) | `--reply-to` |

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
# subject / preheader / from / reply-to all come from the file:
minigun send bulk --list newsletter --md ./week-12.md
```

Rules:

- An **explicit flag always wins**; frontmatter only fills a flag you didn't pass.
- The block is **stripped from the body** before sending, so it never renders into the email.
- It's recognized only when the **first non-empty line is a fence** (three or more dashes, e.g. `---` or `-----`), closed by a later fence line — a horizontal rule mid-document is left alone.
- **Unknown keys are ignored** (so `author:`, `date:`, etc. are harmless), and quoted values (`"…"` / `'…'`) are unquoted.
- This is a CLI/MCP authoring convenience only — the HTTP API and SDKs still take `subject` / `preheader` / `from` / `reply_to` as explicit fields.

### `minigun send cancel <id>`

Unschedule a send that hasn't started yet — i.e. one still in `scheduled` (future-dated) or `queued` — by transitioning it to `cancelled`:

```bash
minigun send cancel s_8Kx29aPqz
```

This is the counterpart to `--send-at`. The guard is race-safe against the dispatcher: a send that has already started (`running`) or finished (`completed`/`failed`/`cancelled`) returns `409` and is left untouched.

### `minigun send status <id>`

One-shot status:

```bash
minigun send status s_8Kx29aPqz
```

Tail until the send is terminal:

```bash
minigun send status s_8Kx29aPqz --watch --interval 5s
```

### `minigun send stats <id>`

Aggregate stats (Mailgun Metrics API + local unsubscribe count). The server persists these counts to its `send_stats` table on a front-loaded schedule (+0, +1h, +6h, +24h, +48h, +5d after a send completes), so stats survive Mailgun's 5-day event retention:

```bash
minigun send stats s_8Kx29aPqz
```

### `minigun send resume <id>`

```bash
minigun send resume s_8Kx29aPqz
# if a previous run crashed mid-batch:
minigun send resume s_8Kx29aPqz --force
```

`--force` is required when any batch is left in `in_flight` state, since Mailgun may already have accepted it; retrying could duplicate-send.

### `minigun send recipients <id>`

Per-recipient message engagement rollup for a send — one row per contact summarizing how that recipient interacted with the message: `sent_at`, `delivered_at`, first/last open + click with counts, and failure/complaint/unsubscribe state (timestamps are epoch seconds). Requires `EVENTS_ARCHIVE_ENABLED=true` on the server.

```bash
# First page (keyset-paginated by contact_id, default limit 100, max 500):
minigun send recipients s_8Kx29aPqz

# Stream every recipient as one JSON array:
minigun send recipients s_8Kx29aPqz --all > recipients.json
```

This is the per-message detail tier (`contact_message_engagement`). For a contact's lifetime engagement across a whole list use `minigun contact engagement`. The archive cron pulls Mailgun's events API burst-then-daily for 30 days, then `events_archive_complete` flips to 1 and polling stops — but the rollups remain queryable forever. MiniGun keeps no raw per-event log; each event folds straight into the rollups (at most one row per recipient).

> **Note:** a recipient only appears here if it resolves to a known contact. List sends always upsert their recipients, so they're fully covered. A **list-less transactional single to a brand-new address** is *not* stored as a contact, so it won't show up in this rollup.

### `minigun send clicks <id>`

Per-URL click rollup for a send — one row per `(recipient, clicked URL)`: the canonical destination URL, first/last click timestamps, and a click count. This is the per-link detail behind `contact_message_engagement.total_clicks`, intended for segmenting an audience by what they clicked.

```bash
# First page (keyset-paginated by (contact_id, url), default limit 100, max 500):
minigun send clicks s_8Kx29aPqz

# Stream every click row as one JSON array:
minigun send clicks s_8Kx29aPqz --all > clicks.json
```

URLs are stored **canonical**: trimmed, scheme + host lowercased (path case preserved), query string and fragment stripped — so the same destination aggregates regardless of UTM/tracking params or per-recipient tokens. Same coverage caveat as `send recipients`: only clicks by known contacts are recorded. Requires `EVENTS_ARCHIVE_ENABLED=true`.

### `minigun contact engagement <idOrEmail>`

Per-(contact, list) engagement summary: total delivered/opens/clicks, last open + click timestamps, and `messages_since_last_engagement` (the dormancy counter that powers prune-by-count).

```bash
# All lists the contact is on:
minigun contact engagement alice@example.com

# Single list (accepts list slug or id):
minigun contact engagement alice@example.com --list newsletter
minigun contact engagement c_PP5AA3MBXS    --list l_8Kx29aPqz
```

Requires `EVENTS_ARCHIVE_ENABLED=true`. Returns an empty `items` array for contacts who haven't been delivered to yet — rows are sparse on purpose, so a never-emailed contact isn't a false positive for any prune criterion.

### `minigun list prune <list>`

Engagement-based prune. Three OR'd criteria, any combination — but **dry-run is the default**. You must pass `--apply` to commit.

```bash
# Dry-run: 20 wasted deliveries OR 180 days dormant. Returns candidate
# count, a sample (default 25), and a reason_counts breakdown.
minigun list prune newsletter --by-count 20 --by-recency 180

# Dry-run with the third criterion: contacts who've been on the list
# more than 90 days with zero delivered events.
minigun list prune newsletter --no-delivery-for 90

# Commit. Writes one unsubscribe_events audit row per pruned contact
# with the most specific matched reason (count > recency > no-delivery).
minigun list prune newsletter --by-count 20 --by-recency 180 --apply

# Cap the per-call batch (default 1000, max 10000):
minigun list prune newsletter --by-recency 365 --limit 500 --apply
```

`<list>` accepts list slug or id. At least one criterion must be > 0 — calling without any will fail-closed with `400`. Real-runs are bounded; massive backlogs drain over multiple invocations so anomalies surface in the audit log before half the list is gone.

## End-to-end walkthrough

```bash
minigun health
minigun list create --name "Weekly" --slug weekly
minigun contact add weekly alice@example.com --params '{"first_name":"Alice"}'
minigun contact add weekly bob@example.com   --params '{"first_name":"Bob"}'

minigun send bulk \
  --list weekly \
  --subject "Hi {{first_name | \"there\"}}" \
  --from "Ran <ran@example.com>" \
  --md ./week-12.md

# capture the send_id from the output and watch it
minigun send status s_xxxx --watch
minigun send stats  s_xxxx
```

## MCP server (`minigun mcp`)

The same binary speaks the [Model Context Protocol](https://modelcontextprotocol.io) over stdio so AI clients (Claude Desktop, Cursor, Zed, Continue, Goose, etc.) can drive MiniGun in natural language. The MCP subcommand reuses the same `--api`/`--token` configuration as the CLI; no server-side changes required.

```bash
export MINIGUN_API_URL=https://minigun.example.com
export MINIGUN_API_TOKEN=...

minigun mcp < /dev/null    # sanity check — exits immediately on EOF
```

### Client config (Claude Desktop / Cursor)

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

The `env` block is optional if your MCP client inherits the shell environment. macOS GUI applications generally do **not** inherit shell env, so prefer the explicit form there.

### What's exposed

**Tools** — every MiniGun operation as an MCP tool. Destructive ones (`send_bulk`, `send_single`, `unsubscribe_contact`, `resume_send`, `cancel_send`) are tagged so MCP clients can render the appropriate confirmation UI.

| Tool | Maps to | Notes |
|---|---|---|
| `health` | `GET /healthz` | ReadOnly |
| `create_list` | `POST /lists` | |
| `add_contact` | `POST /lists/{list}/contacts` | |
| `unsubscribe_contact` | `POST /lists/{list}/unsubscribe` | Destructive — confirmation suggested |
| `delete_contact` | `DELETE /contacts/{idOrEmail}` | Destructive — hard purge |
| `send_single` | `POST /send/single` | Destructive — sends mail; accepts `send_at` to schedule |
| `send_bulk` | `POST /send/bulk` | Destructive — sends mail; accepts `send_at` to schedule |
| `resume_send` | `POST /send/{id}/resume` | Destructive — sends mail |
| `cancel_send` | `POST /send/{id}/cancel` | Destructive — unschedule a `scheduled`/`queued` send |
| `get_send_status` | `GET /send/{id}` | ReadOnly |
| `get_send_stats` | `GET /send/{id}/stats` | ReadOnly |
| `list_send_recipients` | `GET /send/{id}/recipients` | ReadOnly — per-recipient message engagement; requires `EVENTS_ARCHIVE_ENABLED=true` |
| `list_send_clicks` | `GET /send/{id}/clicks` | ReadOnly — per-URL click rollup for segmentation; requires `EVENTS_ARCHIVE_ENABLED=true` |
| `get_contact_engagement` | `GET /contacts/{idOrEmail}/engagement` | ReadOnly — requires `EVENTS_ARCHIVE_ENABLED=true` |
| `prune_list` | `POST /lists/{list}/prune` | Destructive — `dry_run` defaults to `true` |

**Resources** — enumeration as MCP resources. Paginated resources accept `?cursor=` and `?limit=` query parameters in the URI.

```
minigun://lists
minigun://lists/{slug}
minigun://lists/{slug}/contacts
minigun://sends
minigun://sends/{id}
minigun://sends/{id}/stats
```

**Prompts** — reusable workflows for common operator tasks.

- `compose_newsletter` — drafts a Markdown newsletter using MiniGun's variable conventions (`{{first_name | "there"}}`, etc.).
- `audit_send` — produces a post-send report with delivery, open, click, complaint, and unsubscribe rates.

### Local validation

To poke the MCP server directly:

```bash
npx -y @modelcontextprotocol/inspector minigun mcp
```

This opens a UI that lists all exposed tools, resources, and prompts and lets you invoke them with arbitrary arguments.
