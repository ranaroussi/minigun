# MiniGun PRD

## Overview

MiniGun is a lightweight self-hosted email sending service built on top of Mailgun.

It is designed for developers, indie hackers, founders, and internal teams that want:

- reliable bulk email sending
- contact and unsubscribe management
- simple APIs and CLI workflows
- resumable bulk sends
- Markdown-based authoring
- low operational complexity

MiniGun is not intended to be a full ESP (Email Service Provider).

Instead, it acts as a thin orchestration layer between:

- your application
- your contact database
- Mailgun’s delivery infrastructure

Mailgun remains responsible for:

- email delivery
- open/click tracking
- deliverability
- bounce handling
- reputation management
- analytics

MiniGun handles:

- contacts
- lists
- unsubscribe management
- bulk send orchestration
- batching
- resumable sends
- template preprocessing
- lightweight aggregate reporting

------

# Goals

## Primary goals

- Extremely lightweight
- Portable deployment
- Single binary
- SQLite-based
- Docker-friendly
- Async sending
- Reliable resumable bulk sends
- Simple API
- CLI support
- Markdown-first email authoring
- Mailgun-native delivery

## Non-goals

- Replacing Mailgun analytics
- Real-time event processing
- Full ESP functionality
- Marketing automation
- Visual email builder
- Workflow automation
- Webhook-heavy architecture
- Multi-provider support (initially)

------

# Target users

- Indie hackers
- SaaS founders
- Internal business tools
- Developers
- Agencies
- Small businesses

------

# High-level architecture

```text
CLI / Postman / App
        ↓
MiniGun API
        ↓
SQLite
        ↓
Mailgun API
```

------

# Core features

## Contacts and lists

MiniGun stores contacts locally in SQLite.

Each contact belongs to a list.

Contacts support arbitrary JSON metadata.

Example:

```json
{
  "first_name": "Ran",
  "company": "Automaze"
}
```

### Features

- Add contact to list
- Update contact metadata
- Unsubscribe contact
- Query list statistics
- Per-list subscription management

------

# Database schema

## lists

`name` is the human-readable label (used in copy such as "you've been unsubscribed from {{name}}").

`slug` is the URL-safe identifier used in API paths. Sends accept either `id` or `slug`.

```sql
CREATE TABLE lists (
  id TEXT PRIMARY KEY,
  slug TEXT NOT NULL UNIQUE,
  name TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
```

## contacts

Contacts are globally unique by lowercase email address.

A contact may belong to multiple lists through the subscriptions table.

```sql
CREATE TABLE contacts (
  id TEXT PRIMARY KEY,
  email TEXT NOT NULL UNIQUE,
  params TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
```

## subscriptions

Subscriptions link contacts to lists.

Subscription state lives here, not on the contact record.

`id` is a monotonically increasing integer. Bulk sends rely on this for resumable, deterministic ordering (`WHERE id > last_subscription_id ORDER BY id ASC`).

```sql
CREATE TABLE subscriptions (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  list_id TEXT NOT NULL,
  contact_id TEXT NOT NULL,
  subscribed INTEGER NOT NULL DEFAULT 1,
  subscribed_at TEXT,
  updated_at TEXT NOT NULL,
  unsubscribed_at TEXT,
  UNIQUE(list_id, contact_id)
);
```

## sends

`id` doubles as the Mailgun tag (`o:tag=<send_id>`) so per-send aggregate stats can be retrieved from Mailgun's Metrics API.

`from_header` stores the full RFC 5322 `From` value (e.g. `"Ran <ran@example.com>"`).

`last_subscription_id` is the highest `subscriptions.id` that has been successfully handed off to Mailgun. Resume continues with `WHERE subscriptions.id > last_subscription_id`.

`max_subscription_id` is snapshotted at send creation. Recipients added to the list after a send starts are excluded from that send (the recipient set is frozen).

Open and click tracking are always on (`o:tracking-opens=yes`, `o:tracking-clicks=yes`). Mailgun's unsubscribe tracking is always off — MiniGun owns that flow.

```sql
CREATE TABLE sends (
  id TEXT PRIMARY KEY,
  type TEXT NOT NULL,
  list_id TEXT,
  recipient_email TEXT,
  subject TEXT NOT NULL,
  from_header TEXT NOT NULL,
  reply_to TEXT,
  template_name TEXT,
  status TEXT NOT NULL,
  batch_size INTEGER NOT NULL DEFAULT 500,
  throttle_ms INTEGER NOT NULL DEFAULT 1000,
  last_subscription_id INTEGER NOT NULL DEFAULT 0,
  max_subscription_id INTEGER,
  total_recipients INTEGER DEFAULT 0,
  unsubscribe_mode TEXT NOT NULL DEFAULT 'local',
  unsubscribe_redirect_url TEXT,
  unsubscribe_external_url TEXT,
  notify_email TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  completed_at TEXT
);
```

## send_batches

```sql
CREATE TABLE send_batches (
  id TEXT PRIMARY KEY,
  send_id TEXT NOT NULL,
  batch_index INTEGER NOT NULL,
  start_offset INTEGER NOT NULL,
  end_offset INTEGER NOT NULL,
  status TEXT NOT NULL,
  mailgun_response TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
```

## unsubscribe_events

```sql
CREATE TABLE unsubscribe_events (
  id TEXT PRIMARY KEY,
  send_id TEXT,
  subscription_id INTEGER NOT NULL,
  list_id TEXT NOT NULL,
  contact_id TEXT NOT NULL,
  email TEXT NOT NULL,
  created_at TEXT NOT NULL
);
```

------

# API

## Create list

```http
POST /lists
```

Payload:

```json
{
  "name": "Weekly Newsletter",
  "slug": "newsletter"
}
```

Response:

```json
{
  "id": "l_8Kx29aPqz",
  "slug": "newsletter",
  "name": "Weekly Newsletter"
}
```

Behavior:

- `name` is human-readable, used in copy ("you've been unsubscribed from Weekly Newsletter")
- `slug` is URL-safe, lowercased, unique
- Returns 409 if `slug` already exists
- All subsequent list-scoped endpoints accept either `id` or `slug` in `:list`

------

## Add contact

```http
POST /lists/:list/contacts
```

Payload:

```json
{
  "email": "ran@example.com",
  "params": {
    "first_name": "Ran",
    "company": "Automaze"
  }
}
```

Behavior:

- Resolves `:list` by `id` or `slug`; returns 404 if list does not exist (lists are not auto-created)
- Lowercases and normalizes email
- Inserts or updates contact by email
- Creates or updates subscription for the list
- Marks subscription `subscribed=1`, sets `subscribed_at` if previously unsubscribed

------

## Unsubscribe

```http
POST /lists/:list/unsubscribe
```

Payload:

```json
{
  "email": "ran@example.com"
}
```

Behavior:

- Marks contact unsubscribed
- Records unsubscribe event

------

## Bulk send

```http
POST /send/bulk
```

Payload:

```json
{
  "list": "newsletter",
  "subject": "Weekly update",
  "from": "Ran <ran@example.com>",
  "reply_to": "support@example.com",
  "md": "# Hello",
  "template": "default",
  "batch_size": 500,
  "throttle_ms": 1000,
  "notify_email": "ran@example.com"
}
```

Response:

```json
{
  "send_id": "s_8Kx29aPqz",
  "status": "queued"
}
```

Behavior:

- Async processing
- Immediately returns `send_id`
- Background worker handles sending
- Supports resume
- Sends completion notification email optionally
- `template` refers to a MiniGun-side HTML wrapper template (not Mailgun's stored templates feature)
- `from` and `reply_to` are persisted as `from_header` and `reply_to` and forwarded to Mailgun as the `from` form field and `h:Reply-To` header respectively
- `send_id` is used as the Mailgun tag (`o:tag=<send_id>`) so per-send stats can be queried later via Mailgun's Metrics API

------

## Single send

```http
POST /send/single
```

Payload:

```json
{
  "to": "ran@example.com",
  "subject": "Test",
  "from": "Ran <ran@example.com>",
  "md": "# Hello"
}
```

Behavior:

- Synchronous; posts directly to Mailgun `POST /v3/<domain>/messages`
- Does **not** use `recipient-variables` (Mailgun treats it as a normal one-recipient message)
- Still persists a row in `sends` with `type='single'` and `recipient_email` set
- Still tagged with `o:tag=<send_id>` for stats parity

------

## Resume send

```http
POST /send/:id/resume
```

Behavior:

- Rejects with 409 if `status` is `running`, `completed`, or `cancelled`
- Continues with `WHERE subscriptions.id > sends.last_subscription_id AND subscriptions.id <= sends.max_subscription_id AND subscribed = 1`
- Any `send_batches` row left in `in_flight` is treated as suspect: the operator must explicitly confirm before it is re-attempted (avoids duplicate sends if the worker crashed mid-batch)
- The recipient set is the one frozen at original send creation (via `max_subscription_id`); no recipients added since then are included

------

## Send status

```http
GET /send/:id
```

Example response:

```json
{
  "id": "s_8Kx29aPqz",
  "status": "running",
  "progress": {
    "completed_batches": 14,
    "total_batches": 49,
    "sent": 14000,
    "remaining": 34920,
    "last_subscription_id": 482310
  }
}
```

------

## Send stats

```http
GET /send/:id/stats
```

Example response:

```json
{
  "id": "s_8Kx29aPqz",
  "sent": 48231,
  "delivered": 47680,
  "failed": 312,
  "opened": 12840,
  "clicked": 921,
  "complained": 4,
  "unsubscribed": 118
}
```

Behavior:

- Mailgun-sourced fields (`delivered`, `failed`, `opened`, `clicked`, `complained`) are pulled from Mailgun's **Metrics API** (`POST /v1/analytics/metrics`), filtered by `tag = <send_id>`. Mailgun's legacy Stats and Events APIs are deprecated and are not used.
- `unsubscribed` is sourced locally from `unsubscribe_events WHERE send_id = ?` (Mailgun's unsubscribe tracking is disabled — MiniGun owns this).
- `sent` is sourced locally from `send_batches` (sum of successful batch sizes).
- No real-time analytics processing; this endpoint is a thin pull/merge.

Example Mailgun Metrics request body MiniGun emits:

```json
{
  "start": "2026-01-01T00:00:00Z",
  "end":   "2026-01-08T00:00:00Z",
  "resolution": "day",
  "metrics": ["accepted_count","delivered_count","failed_count","opened_count","clicked_count","complained_count"],
  "filter": { "AND": [ { "attribute": "tag", "comparator": "=", "values": [{ "label": "s_8Kx29aPqz", "value": "s_8Kx29aPqz" }] } ] }
}
```

------

# Content authoring

## Supported input modes

### Markdown mode

```json
{
  "md": "# Hello"
}
```

Behavior:

- Markdown → HTML
- Markdown → plain text

### HTML mode

```json
{
  "html": "<h1>Hello</h1>",
  "text": "Hello"
}
```

------

# Templates

Optional wrapper templates may be used.

Example:

```html
<html>
  <body>
    {{content}}
  </body>
</html>
```

Supported variables:

```text
{{content}}
{{subject}}
{{preheader}}
```

------

# Variable replacement

Authors may use placeholders directly inside Markdown or HTML.

Example:

```text
Hi {{first_name | "there"}},
```

Behavior:

1. MiniGun extracts placeholders
2. Detects fallback values
3. Fills missing recipient variables
4. Converts placeholders to Mailgun recipient variables

Final transformed content:

```text
Hi %recipient.first_name%,
```

Recipient variables:

```json
{
  "first_name": "Ran"
}
```

Or fallback:

```json
{
  "first_name": "there"
}
```

------

# Unsubscribe system

MiniGun fully owns the unsubscribe flow. Mailgun's own unsubscribe tracking is **always disabled** so that suppression state lives in exactly one place (SQLite).

## How Mailgun tracking is suppressed

On every outbound batch MiniGun sends:

- `o:tracking-unsubscribe=no` (disables Mailgun's automatic unsubscribe footer injection)
- `o:tracking-opens=yes`
- `o:tracking-clicks=yes`
- `h:List-Unsubscribe=<%recipient.unsub_url%>` (RFC 2369 one-click)
- `h:List-Unsubscribe-Post=List-Unsubscribe=One-Click` (RFC 8058, required by Gmail/Yahoo bulk-sender rules)
- `o:tag=<send_id>` (also exposed as `v:minigun_send_id=<send_id>` for redundancy)

Authors must **never** use `%unsubscribe_url%` in their templates — that is Mailgun's variable. MiniGun's variable is `%recipient.unsub_url%`.

## Per-recipient unsubscribe URL

MiniGun emits one URL per recipient via Mailgun's `recipient-variables`:

```text
https://minigun.example.com/u/<token>
```

Where `<token>` is a URL-safe encoding of `<send_id>.<subscription_id>.<hmac>`:

- `send_id` — the `sends.id` value
- `subscription_id` — the integer `subscriptions.id`
- `hmac` — HMAC-SHA256(secret, "<send_id>:<subscription_id>") truncated to 16 bytes, base64url-encoded

The secret comes from `MINIGUN_HMAC_SECRET`. Tokens are stateless — no DB lookup required for verification, no `tokens` table.

The base URL comes from `MINIGUN_PUBLIC_URL`.

`recipient-variables` payload (per recipient):

```json
{
  "ran@example.com": {
    "first_name": "Ran",
    "unsub_url": "https://minigun.example.com/u/s_8Kx29aPqz.482310.7f2c..."
  }
}
```

## Prefetch protection (two-step flow + CAPTCHA)

Email security scanners (Microsoft Defender, Gmail link inspection, Proofpoint, Mimecast) and link prefetchers (Apple Mail Privacy Protection, iOS Mail, some webmail clients) will automatically follow links in messages before the recipient sees them. A naive `GET /u/:token` that unsubscribes on read would be silently triggered by these scanners.

MiniGun therefore uses a two-step flow protected by a CAPTCHA challenge. **A `GET` never has side effects.**

### Flow

1. **`GET /u/:token`** — verifies the token's HMAC, then renders a confirmation page containing the list name, the recipient email, a single "Confirm unsubscribe" button, and a Cloudflare Turnstile widget. No subscription state is mutated.
2. **`POST /u/:token`** (form-encoded, with the Turnstile response) — verifies the token's HMAC, verifies the Turnstile token server-side against Cloudflare's siteverify endpoint, then performs the unsubscribe and applies the send's `unsubscribe_mode` (renders the confirmation page, or 302-redirects, per mode).
3. **`POST /u/:token`** with body `List-Unsubscribe=One-Click` (RFC 8058) — **bypasses the CAPTCHA**. These requests come from mailbox providers (Gmail, Yahoo, Apple Mail) honoring the `List-Unsubscribe` header, never from prefetchers, and RFC 8058 mandates immediate idempotent unsubscribe with `200 OK`. The request is identified by its `Content-Type: application/x-www-form-urlencoded` body exactly matching the RFC 8058 form.

### CAPTCHA choice

Cloudflare Turnstile is the default because it is free, privacy-respecting (no Google dependency), and invisible to the overwhelming majority of real users. If `MINIGUN_TURNSTILE_SITE_KEY` is not set, MiniGun still renders the two-step confirmation page (which alone blocks GET-based prefetchers), just without the bot challenge — this keeps the deployment story zero-config for hobby use while letting production users harden it.

## Unsubscribe modes

Each send may define its own unsubscribe behavior, stored on the `sends` row (`unsubscribe_mode`, `unsubscribe_redirect_url`, `unsubscribe_external_url`).

All modes share the same `GET /u/:token` confirmation page (described above). Modes only differ in what happens **after** the user confirms via `POST /u/:token`.

### Default local unsubscribe (`mode = "local"`)

After confirmation:

1. Subscription is marked `subscribed=0`, `unsubscribed_at` set
2. `unsubscribe_events` row inserted with `send_id`, `subscription_id`, `list_id`, `contact_id`, `email`
3. Built-in confirmation page displayed:

```text
You've been unsubscribed from {{list.name}}.
```

------

### Redirect after unsubscribe (`mode = "redirect"`)

Configured on the send:

```json
{
  "unsub_redir": "https://example.com/goodbye"
}
```

After confirmation:

1. MiniGun unsubscribes the subscription locally
2. Records unsubscribe event
3. 302 redirects to `unsubscribe_redirect_url`, appending safe query params:

```text
https://example.com/goodbye?email=ran@example.com&list=newsletter
```

------

### External unsubscribe handler (`mode = "external"`)

Configured on the send:

```json
{
  "unsub_url": "https://example.com/unsubscribe"
}
```

After confirmation:

1. MiniGun 302 redirects to `unsubscribe_external_url` with the same safe query params
2. **No local unsubscribe is recorded** — the external handler is expected to call `POST /lists/:list/unsubscribe` (admin endpoint) to inform MiniGun. This is the only mode where suppression state can drift; document it explicitly.

> Note: RFC 8058 one-click POSTs (from mailbox providers) **always** perform the local unsubscribe regardless of `mode`, since those requests cannot follow a redirect and mailbox providers expect a flat `200 OK`. In `external` mode this means suppression is recorded locally for one-click flows; ensure the external system reconciles via `GET /lists/:list/unsubscribes` (future endpoint) or by accepting MiniGun's webhook.

------

# Async sending

Bulk sends are always asynchronous.

Behavior:

```text
POST /send/bulk
→ returns immediately
→ background worker processes send
```

Benefits:

- avoids reverse proxy timeouts
- enables resumable sending
- simplifies deployment platforms
- allows status tracking

------

# Batch sending

MiniGun calls `POST https://<api_base>/v3/<MAILGUN_DOMAIN>/messages` with HTTP Basic auth (`api:<MAILGUN_API_KEY>`), using `multipart/form-data` (required for `recipient-variables`).

Defaults:

```text
batch_size = 500
throttle_ms = 1000
```

`batch_size=500` (rather than Mailgun's hard ceiling of 1000) is intentional — it keeps each request well under Mailgun's ~30 MB body limit even with reasonably-sized per-recipient variables.

Per-batch form fields MiniGun emits:

```text
from                          = <sends.from_header>
to                            = <comma-joined recipient emails for this batch>
subject                       = <sends.subject>
text                          = <rendered plain text>
html                          = <rendered HTML, with %recipient.X% placeholders>
recipient-variables           = <JSON map keyed by recipient email>
o:tag                         = <send_id>
o:tracking                    = yes
o:tracking-opens              = yes
o:tracking-clicks             = yes
o:tracking-unsubscribe        = no
h:Reply-To                    = <sends.reply_to>            (when set)
h:List-Unsubscribe            = <%recipient.unsub_url%>
h:List-Unsubscribe-Post       = List-Unsubscribe=One-Click
v:minigun_send_id             = <send_id>
v:minigun_subscription_ids    = <comma-joined subscription IDs in this batch>
```

`recipient-variables` is a JSON object keyed by recipient email. To stay safely below Mailgun's request-body limit, MiniGun only includes the variables actually referenced by the rendered template — never the full `contacts.params` blob.

Per-batch worker loop:

```text
1. Claim next chunk: SELECT subscriptions.id, contact_id, email, params
                     FROM subscriptions JOIN contacts ON contact_id = contacts.id
                     WHERE list_id = ?
                       AND subscribed = 1
                       AND subscriptions.id > sends.last_subscription_id
                       AND subscriptions.id <= sends.max_subscription_id
                     ORDER BY subscriptions.id ASC
                     LIMIT batch_size
2. Insert send_batches row with status='in_flight', start_subscription_id, end_subscription_id
3. Build recipient-variables JSON (only referenced template vars + unsub_url)
4. POST to Mailgun. Retry 5xx and 429 with exponential backoff.
5. On 2xx:
     - send_batches.status = 'succeeded', store Mailgun's message id in mailgun_response
     - UPDATE sends SET last_subscription_id = end_subscription_id
   On terminal 4xx (other than 429):
     - send_batches.status = 'failed', store the Mailgun error body
     - Mark send.status='failed', stop the worker
6. Sleep throttle_ms
7. If more rows remain, loop. Otherwise mark send.status='completed'.
```

------

# Resume support

Resume is the natural consequence of the schema, not a separate machinery:

- `sends.last_subscription_id` is the strictly-monotonic cursor
- `sends.max_subscription_id` is the frozen ceiling
- `subscriptions.id` is a monotonically increasing integer

On failure or restart:

```text
minigun send resume <send_id>
```

Behavior:

- `sends.last_subscription_id` is only advanced after Mailgun returns 2xx for that batch, so the cursor by definition reflects "successfully handed off"
- Any `send_batches` row in `in_flight` is suspect (worker crashed mid-call): require an explicit `--force` to retry, since Mailgun may have already accepted the request
- A resumed send does **not** re-render or re-batch already-completed work — it just continues from `last_subscription_id + 1` with the same `max_subscription_id` ceiling, so the recipient set stays frozen at original creation time

------

# Notifications

Optional completion notifications.

Example:

```json
{
  "notify_email": "ran@example.com"
}
```

Behavior:

- sends email when send completes
- sends email on failure
- includes summary statistics

------

# CLI

The CLI acts as an API client.

Examples:

```bash
minigun list create \
  --name "Weekly Newsletter" \
  --slug newsletter
minigun contact add newsletter ran@example.com \
  --params '{"first_name":"Ran"}'
minigun send bulk \
  --list newsletter \
  --md email.md \
  --subject "Weekly update" \
  --from "Ran <ran@example.com>"
minigun send single \
  --to ran@example.com \
  --md email.md \
  --from "Ran <ran@example.com>"
minigun send resume s_8Kx29aPqz
```

`subject`, `from`, `template`, `reply_to`, etc. are persisted on the `sends` row at creation; `minigun send resume` does not re-accept them.

------

# Health check

```http
GET /healthz
```

Returns `200 OK` with `{"status":"ok","db":"ok"}` when SQLite is reachable. Used by Docker `HEALTHCHECK`, k8s probes, and uptime monitors.

------

# Rate limits and error handling

MiniGun assumes Mailgun is the source of truth for sending limits and treats its responses as follows:

- **2xx** — success. Advance `last_subscription_id`, persist Mailgun's message id.
- **429 Too Many Requests** — back off and retry the *same* batch. Exponential backoff with jitter, capped at 5 attempts. Honor `Retry-After` if present.
- **5xx** — retryable infrastructure error. Same backoff policy as 429.
- **4xx (other than 429)** — terminal. Mark the batch `failed`, mark the send `failed`, write the Mailgun error body to `send_batches.mailgun_response`, fire the failure notification email (if `notify_email` is set), and stop the worker.

The worker only ever has one in-flight batch per send, which keeps reasoning about Mailgun's per-second rate limits straightforward.

------

# Recommended tech stack

## Backend

- Go
- chi router
- cobra CLI
- SQLite
- goose migrations
- goldmark Markdown parser

## Deployment

- Single Go binary
- Docker support
- SQLite persistent volume

## Infrastructure

- Mailgun API
- SQLite database
- Local filesystem

------

# Configuration

| Env var                | Required | Default                  | Purpose |
|------------------------|----------|--------------------------|---------|
| `MAILGUN_API_KEY`      | yes      | —                        | Mailgun API key. Sent as HTTP Basic password (user is `api`). |
| `MAILGUN_DOMAIN`       | yes      | —                        | Sending domain. Used in `POST /v3/<domain>/messages`. |
| `MAILGUN_REGION`       | no       | `us`                     | `us` or `eu`. Selects `https://api.mailgun.net` vs `https://api.eu.mailgun.net`. |
| `MAILGUN_API_BASE`     | no       | derived from region      | Explicit override for the API base URL. |
| `MINIGUN_PUBLIC_URL`   | yes      | —                        | Public origin used to build per-recipient unsubscribe URLs. |
| `MINIGUN_HMAC_SECRET`  | yes      | —                        | Secret used to HMAC-sign unsubscribe tokens. |
| `MINIGUN_DB_PATH`      | no       | `/data/minigun.db`       | SQLite file path. |
| `MINIGUN_LISTEN_ADDR`  | no       | `:8080`                  | HTTP listen address. |
| `MINIGUN_TURNSTILE_SITE_KEY`   | no | —                       | Cloudflare Turnstile site key. If unset, the unsubscribe confirmation page is still rendered (two-step flow defeats prefetchers), but no bot challenge is shown. |
| `MINIGUN_TURNSTILE_SECRET_KEY` | no | —                       | Cloudflare Turnstile secret. Required when `MINIGUN_TURNSTILE_SITE_KEY` is set; used server-side against `https://challenges.cloudflare.com/turnstile/v0/siteverify`. |

------

# Deployment example

```bash
docker run \
  -d \
  --restart unless-stopped \
  -p 8080:8080 \
  -v ./data:/data \
  -e MAILGUN_API_KEY=... \
  -e MAILGUN_DOMAIN=mg.example.com \
  -e MAILGUN_REGION=us \
  -e MINIGUN_PUBLIC_URL=https://minigun.example.com \
  -e MINIGUN_HMAC_SECRET=$(openssl rand -hex 32) \
  -e MINIGUN_TURNSTILE_SITE_KEY=0x4AAA... \
  -e MINIGUN_TURNSTILE_SECRET_KEY=0x4AAA... \
  ghcr.io/ranaroussi/minigun
```

------

# Future ideas

## Potential future features

- Web UI
- Multiple Mailgun domains
- Multiple providers
- Scheduled sends
- A/B testing
- Send previews
- Attachment support
- Bounce syncing
- Tag management
- Domain warmup tooling
- Per-contact send history

------

# Philosophy

MiniGun intentionally avoids becoming a full email platform.

The philosophy is:

```text
Keep sending simple.
Let Mailgun do the heavy lifting.
Own your contacts and unsubscribe flow.
```