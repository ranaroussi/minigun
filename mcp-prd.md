# MiniGun MCP PRD

## Overview

MiniGun MCP exposes the MiniGun HTTP API as an [MCP](https://modelcontextprotocol.io) server so that AI clients (Claude Desktop, Cursor, Zed, Continue, Goose, etc.) can drive list management, contact upserts, sends, and analytics through natural language.

The MCP layer is implemented as a subcommand of the existing CLI (`minigun mcp`) using the official [`github.com/modelcontextprotocol/go-sdk`](https://github.com/modelcontextprotocol/go-sdk). It runs locally on the operator's machine over stdio, talks to a remote MiniGun server over HTTPS, and reuses the CLI's HTTP client and configuration. The server itself ships no MCP code.

------

# Goals

## Primary goals

- Expose every existing MiniGun operation as an MCP **tool**.
- Expose enumeration of lists, contacts, and sends as MCP **resources**.
- Provide reusable **prompts** for the two most common operator workflows (composing a newsletter, auditing a finished send).
- Single binary (`minigun`) houses both the human CLI and the MCP server.
- Zero server-side changes that are MCP-specific. Listing endpoints added for resources are independently useful.
- Configuration is the same as the CLI: `MINIGUN_API_URL`, `MINIGUN_API_TOKEN`, plus `--api` / `--token` flags. No new environment variables.

## Non-goals

- Multi-tenant MCP serving. MiniGun is single-operator by design; each operator runs their own local MCP process.
- Server-side MCP transport (`/mcp` route on the HTTP server). Not ruled out for the future, but not in this scope.
- Replacing the human CLI. The MCP subcommand sits alongside, sharing internals.
- Built-in confirmation prompts inside the MCP server. We rely on the LLM client's existing tool-call approval UI as the human-in-the-loop.

------

# Architecture

```
                 ┌───────────────────────────┐
                 │  Claude Desktop / Cursor  │
                 │  / Zed / Continue / ...   │
                 └────────────┬──────────────┘
                              │ stdio (JSON-RPC)
                              │
                 ┌────────────▼──────────────┐
                 │  minigun mcp              │   ← lives in cli/
                 │  ───────────              │
                 │  official mcp-go-sdk      │
                 │  tools + resources +      │
                 │  prompts                  │
                 │                           │
                 │  cli/internal/client      │   ← same HTTP client
                 │  (Bearer-token auth)      │      the CLI uses
                 └────────────┬──────────────┘
                              │ HTTPS
                              │
                 ┌────────────▼──────────────┐
                 │  MiniGun server           │
                 │  (REST API in src/)       │
                 │  Open today, bearer auth  │
                 │  recommended before       │
                 │  exposing publicly        │
                 └───────────────────────────┘
```

Properties this gives us:

- **Stdio transport** is universally supported by every MCP client today. No `mcp-remote` shim required.
- **Single source of truth for transport** — the CLI's `internal/client` package is reused verbatim.
- **Secrets never leave the operator's machine.** The bearer token lives in their MCP client config or shell environment.
- **Fast iteration loop.** Adding a tool or tweaking a description means rebuilding the CLI, not redeploying the server.

------

# Configuration

The MCP subcommand resolves configuration in the same precedence order as the CLI:

1. Explicit flags: `--api`, `--token`
2. Environment variables: `MINIGUN_API_URL`, `MINIGUN_API_TOKEN`
3. Defaults: `MINIGUN_API_URL = http://127.0.0.1:8080`, `MINIGUN_API_TOKEN = ""`

The `env` block in an MCP client config is therefore **optional**. If the user has already exported the variables in the environment that launches the MCP client (and that environment propagates to subprocesses), no `env` block is needed.

## Minimal client config (relies on inherited environment)

```json
{
  "mcpServers": {
    "minigun": {
      "command": "minigun",
      "args": ["mcp"]
    }
  }
}
```

## Explicit client config (for GUI MCP clients that do not inherit shell env)

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

> Note: On macOS, GUI applications (including Claude Desktop) do **not** inherit shell environment variables unless they are set via `launchctl setenv` or an equivalent mechanism. The explicit `env` block is the most reliable approach for those clients. This is a quirk of the host OS, not of MiniGun.

------

# Tools

All tools are JSON-in / JSON-out. Inputs are validated against the schema before the handler runs. Outputs are passed through verbatim from the server.

| Tool                  | Backed by                            | Destructive |
|-----------------------|--------------------------------------|-------------|
| `health`              | `GET /healthz`                       | no          |
| `create_list`         | `POST /lists`                        | no          |
| `add_contact`         | `POST /lists/{list}/contacts`        | no          |
| `unsubscribe_contact` | `POST /lists/{list}/unsubscribe`     | **yes**     |
| `send_single`         | `POST /send/single`                  | **yes**     |
| `send_bulk`           | `POST /send/bulk`                    | **yes**     |
| `resume_send`         | `POST /send/{id}/resume`             | **yes**     |
| `get_send_status`     | `GET /send/{id}`                     | no          |
| `get_send_stats`      | `GET /send/{id}/stats`               | no          |

Destructive tools must say so in their description so that LLM clients can render the appropriate confirmation UI. The string `DESTRUCTIVE` appears at the start of each destructive tool's description.

## Tool definitions

### `health`

```
Description:
  Checks whether the MiniGun server is reachable and its database is
  healthy. Returns {status, db}. Useful as a connection sanity check
  before performing other operations.

Input schema: {} (no inputs)

Returns: { "status": "ok", "db": "ok" }
```

### `create_list`

```
Description:
  Creates a new mailing list. Idempotent on slug — returns an error if
  the slug is already in use. The slug is used in URLs (e.g.
  /lists/{slug}/contacts) and must be lowercase alphanumerics or
  hyphens, 1-64 characters.

Input schema:
  {
    "type": "object",
    "properties": {
      "name": { "type": "string", "description": "Human-readable list name, used in unsubscribe copy" },
      "slug": { "type": "string", "description": "URL-safe slug, lowercase alphanumerics or hyphens" }
    },
    "required": ["name", "slug"]
  }
```

### `add_contact`

```
Description:
  Adds a contact to a list or updates an existing contact's parameters.
  Idempotent on (list, email). If the contact was previously
  unsubscribed from this list, this resubscribes them. `params` is a
  free-form JSON object of contact data used for template variable
  substitution in sends, e.g. {"first_name": "Ran"}.

Input schema:
  {
    "type": "object",
    "properties": {
      "list":   { "type": "string", "description": "List id or slug" },
      "email":  { "type": "string", "format": "email" },
      "params": { "type": "object", "additionalProperties": true }
    },
    "required": ["list", "email"]
  }
```

### `unsubscribe_contact`

```
Description:
  DESTRUCTIVE. Marks a contact as unsubscribed on the given list. The
  contact will not receive future bulk sends to this list until
  explicitly resubscribed via add_contact. Use this only for
  admin-driven unsubscribes; end-user unsubscribes happen via the
  public /u/<token> page.

Input schema:
  {
    "type": "object",
    "properties": {
      "list":  { "type": "string" },
      "email": { "type": "string", "format": "email" }
    },
    "required": ["list", "email"]
  }
```

### `send_single`

```
Description:
  DESTRUCTIVE. Sends a single transactional email to one recipient via
  Mailgun. Synchronous. Returns immediately with a send_id which can be
  used with get_send_status and get_send_stats.

Input schema:
  {
    "type": "object",
    "properties": {
      "to":       { "type": "string", "format": "email" },
      "subject":  { "type": "string" },
      "from":     { "type": "string", "description": "RFC 5322 From header" },
      "reply_to": { "type": "string" },
      "md":       { "type": "string", "description": "Markdown body. One of md or html is required." },
      "html":     { "type": "string", "description": "HTML body. Used if md is not provided." },
      "text":     { "type": "string", "description": "Plain-text body. Auto-generated from md/html if omitted." }
    },
    "required": ["to", "subject", "from"]
  }
```

### `send_bulk`

```
Description:
  DESTRUCTIVE. Initiates an asynchronous bulk email send to all
  currently subscribed contacts on a list. Cannot be undone once
  batches start delivering. The recipient set is FROZEN at send
  creation time — contacts added after the send starts are not
  included. Returns immediately with a send_id; use get_send_status to
  monitor progress and get_send_stats once delivery completes.

  Before calling this, consider calling get_send_status afterward to
  confirm the send is progressing as expected. The default batch_size
  is 500 and throttle_ms is 1000.

Input schema:
  {
    "type": "object",
    "properties": {
      "list":         { "type": "string" },
      "subject":      { "type": "string" },
      "preheader":    { "type": "string" },
      "from":         { "type": "string" },
      "reply_to":     { "type": "string" },
      "md":           { "type": "string" },
      "html":         { "type": "string" },
      "text":         { "type": "string" },
      "template":     { "type": "string", "description": "Wrapper template name (server-side)" },
      "batch_size":   { "type": "integer", "minimum": 1, "maximum": 1000 },
      "throttle_ms":  { "type": "integer", "minimum": 0 },
      "notify_email": { "type": "string", "format": "email", "description": "Email to notify on completion or failure" },
      "unsub_mode":   { "type": "string", "enum": ["local", "redirect", "external"] },
      "unsub_redir":  { "type": "string", "description": "Redirect URL when unsub_mode = redirect" },
      "unsub_url":    { "type": "string", "description": "External handler URL when unsub_mode = external" }
    },
    "required": ["list", "subject", "from"]
  }
```

### `resume_send`

```
Description:
  DESTRUCTIVE. Resumes a paused or failed bulk send from where it left
  off. The recipient set remains frozen at the ORIGINAL send creation
  time. If any batches are stuck in 'in_flight' state from a crashed
  worker, this refuses to resume unless force=true (which may cause
  duplicate delivery, since Mailgun may already have accepted the
  in-flight batch).

Input schema:
  {
    "type": "object",
    "properties": {
      "send_id": { "type": "string" },
      "force":   { "type": "boolean", "default": false }
    },
    "required": ["send_id"]
  }
```

### `get_send_status`

```
Description:
  Returns the current status and progress of a send: status,
  completed_batches, total_batches, sent, remaining,
  last_subscription_id. Use this to monitor a long-running send.

Input schema:
  {
    "type": "object",
    "properties": { "send_id": { "type": "string" } },
    "required":   ["send_id"]
  }
```

### `get_send_stats`

```
Description:
  Returns aggregate stats for a send: sent (from MiniGun),
  delivered/failed/opened/clicked/complained (from Mailgun Metrics
  API), unsubscribed (from MiniGun's local unsubscribe_events).
  Mailgun-sourced fields may lag by up to 1-2 hours.

Input schema:
  {
    "type": "object",
    "properties": { "send_id": { "type": "string" } },
    "required":   ["send_id"]
  }
```

------

# Resources

Resources are read-only. They let the LLM enumerate state without guessing IDs.

| Resource URI                                   | Backed by                                       |
|------------------------------------------------|-------------------------------------------------|
| `minigun://lists`                              | `GET /lists`                                    |
| `minigun://lists/{slug}`                       | `GET /lists/{list}`                             |
| `minigun://lists/{slug}/contacts?cursor=&limit=` | `GET /lists/{list}/contacts?cursor=&limit=`   |
| `minigun://sends?cursor=&limit=`               | `GET /sends?cursor=&limit=`                     |
| `minigun://sends/{id}`                         | `GET /send/{id}`                                |
| `minigun://sends/{id}/stats`                   | `GET /send/{id}/stats`                          |

The MCP server uses the SDK's `list_resources` to advertise `minigun://lists` and `minigun://sends` (the two enumerations) at the top level. The per-item resources are templated and discovered by drilling into those enumerations.

Pagination uses opaque `cursor` strings returned in each list response (`next_cursor`). The MCP layer is a pass-through.

------

# Prompts

Two reusable prompts ship with the MCP server. They are not strictly necessary (the LLM can compose without them), but they prime the model with MiniGun-specific conventions that would otherwise have to be discovered from tool descriptions.

## `compose_newsletter`

Pre-conditions the model to draft a newsletter body in Markdown using MiniGun's variable conventions. Inputs:

```
list: string (required)   — the list slug, so the prompt can include available contact params
goal: string (optional)   — what the newsletter is about
audience_notes: string (optional)
```

The prompt body (paraphrased):

> You are drafting a marketing or transactional newsletter for the
> MiniGun list `{list}`. Use Markdown, not HTML, unless explicitly
> asked otherwise. Personalization variables use the syntax
> `{{var | "fallback"}}` (e.g. `{{first_name | "there"}}`). Do not
> include an unsubscribe link manually — MiniGun injects a
> per-recipient `%recipient.unsub_url%` automatically via the
> List-Unsubscribe header and any `{{unsub_url}}` variable.
>
> Begin by reading `minigun://lists/{list}` to see how many subscribers
> exist and what kinds of contact params are available. Then propose
> a subject line, a preheader, and a body. Ask the operator for
> approval before calling `send_bulk`.

## `audit_send`

Given a `send_id`, walks the model through producing a post-send report. Inputs:

```
send_id: string (required)
```

The prompt body (paraphrased):

> You are auditing a completed MiniGun send. Read
> `minigun://sends/{send_id}` for the send metadata and
> `minigun://sends/{send_id}/stats` for aggregate metrics. Compute and
> report:
>   - delivery rate = delivered / sent
>   - open rate = opened / delivered
>   - click rate = clicked / opened (and clicked / delivered)
>   - complaint rate = complained / delivered
>   - unsubscribe rate = unsubscribed / delivered
> Flag anomalies: complaint rate > 0.1%, unsubscribe rate > 1%, open
> rate < 5%, delivery rate < 95%. Suggest one or two concrete follow-
> ups (e.g., remove repeat-complainers, A/B-test subject line, segment
> further). Be specific and brief.

------

# Server-side prerequisites

The MCP layer is otherwise self-contained, but four new listing endpoints are required for resources to be meaningful. These are not MCP-specific — they are independently useful for the human CLI, future dashboards, and operator scripts.

| Method | Path                                            | Returns |
|--------|-------------------------------------------------|---------|
| GET    | `/lists`                                        | `[{ id, slug, name, subscribed_count, created_at, updated_at }]` |
| GET    | `/lists/{list}`                                 | One list with `subscribed_count`, `total_count`, `last_send_at` |
| GET    | `/lists/{list}/contacts?cursor=&limit=`         | `{ items: [...], next_cursor }`, paginated by `subscriptions.id` |
| GET    | `/sends?cursor=&limit=`                         | `{ items: [...], next_cursor }`, ordered by `created_at DESC` |

Pagination is cursor-based on `subscriptions.id` (for contacts) and `sends.created_at + id` (for sends). Default `limit` is 50, max 500.

These should be added to `src/internal/api/lists.go` and a new `src/internal/api/sends_list.go`. No schema migrations needed.

## Auth

Bearer-token middleware (`Authorization: Bearer <MINIGUN_AUTH_TOKEN>`) is recommended but not strictly required for the MCP rollout. Today the API is open; the MCP server simply doesn't send a token. If/when the operator turns on auth, the MCP client config provides the token via `MINIGUN_API_TOKEN`.

## Idempotency (optional, recommended)

`POST /send/bulk` should accept an optional `idempotency_key` field. If present, the server checks for an existing `sends` row created in the last 24 hours with the same key and returns it instead of creating a new one. This protects against the LLM accidentally retrying a send. The MCP layer auto-generates and supplies an idempotency_key per tool call.

Schema impact:

```sql
ALTER TABLE sends ADD COLUMN idempotency_key TEXT;
CREATE INDEX idx_sends_idempotency ON sends(idempotency_key) WHERE idempotency_key IS NOT NULL;
```

Treat the idempotency_key index as a soft unique check at the application layer (a partial unique index would be tighter but adds migration complexity).

------

# File layout

```
cli/
├── cmd/
│   ├── mcp.go                          # new — `minigun mcp` subcommand
│   ├── root.go                         # existing
│   ├── list.go                         # existing
│   ├── contact.go                      # existing
│   ├── send.go                         # existing
│   └── health.go                       # existing
├── internal/
│   ├── client/                         # existing — reused verbatim
│   │   └── client.go
│   └── mcp/                            # new
│       ├── server.go                   # SDK bootstrap, stdio transport, registers everything
│       ├── tools.go                    # 9 tool handlers
│       ├── resources.go                # resource read handlers + URI parsing
│       ├── prompts.go                  # 2 prompt definitions
│       └── schema.go                   # JSON schemas (hand-written for stability)
├── go.mod                              # add github.com/modelcontextprotocol/go-sdk
├── main.go
└── README.md
```

```
src/internal/api/
├── lists.go                            # add GET /lists, GET /lists/{list}
├── lists_contacts.go                   # new — GET /lists/{list}/contacts (paginated)
└── sends_list.go                       # new — GET /sends (paginated)
```

------

# Implementation plan

Sequenced for a single-day delivery, depending on which order keeps the loop tight.

### Phase 1 — Server-side listing endpoints (~half day)

1. Add cursor-based pagination helper in `src/internal/store/`.
2. Add `GET /lists`, `GET /lists/{list}`, `GET /lists/{list}/contacts`, `GET /sends`.
3. Unit tests for pagination (cursor encoding/decoding, limit bounds).

### Phase 2 — CLI MCP bootstrap (~1-2 hours)

1. Add `github.com/modelcontextprotocol/go-sdk` to `cli/go.mod`.
2. Create `cli/cmd/mcp.go` with the `minigun mcp` subcommand. Stdio transport. Reads env/flags identically to the rest of the CLI.
3. Create `cli/internal/mcp/server.go` that boots the SDK server, registers tools/resources/prompts, blocks on the stdio reader.

### Phase 3 — Tools (~half day)

1. `cli/internal/mcp/schema.go` — JSON Schema definitions, one per tool input.
2. `cli/internal/mcp/tools.go` — 9 handlers, each ~10 LOC:
   - decode input
   - build request body
   - call `client.Post` / `client.Get`
   - return `mcp.CallToolResult` with the JSON body as text content

### Phase 4 — Resources (~2 hours)

1. URI parser: validate against `minigun://lists`, `minigun://lists/{slug}`, `minigun://lists/{slug}/contacts`, `minigun://sends`, `minigun://sends/{id}`, `minigun://sends/{id}/stats`.
2. Resource read handler: route by parsed URI to the appropriate `GET` endpoint.
3. Resource list handler: advertise the two enumerations (`minigun://lists`, `minigun://sends`).

### Phase 5 — Prompts (~1 hour)

1. Two prompt definitions per the specs above.
2. Body text stored as plain string constants — no need for templating engine.

### Phase 6 — Testing & validation (~half day)

1. Local sanity: run `minigun mcp` with `@modelcontextprotocol/inspector` connected over stdio. Validate tool listing, resource listing, prompt listing.
2. Real client: configure Claude Desktop with the `minigun mcp` command, point at the local docker MiniGun, exercise the full happy path (`health` → `create_list` → `add_contact` → read `minigun://lists` → `send_bulk` with throwaway domain → `get_send_status`).
3. Add a few unit tests in `cli/internal/mcp/` for URI parsing and tool input validation.

------

# Security

- The MCP subprocess is a pure operator-trust boundary. It holds the bearer token in memory, forwards it on every server call, and exits when the LLM client disconnects.
- The token is never sent to the LLM. Tool descriptions, schemas, and responses are all the LLM ever sees.
- Tool descriptions that mutate state are tagged `DESTRUCTIVE`. The LLM client's confirmation UI is the human-in-the-loop.
- The MCP server itself listens on **stdio only**. No network ports are opened. The only network egress is the HTTPS call to the configured `MINIGUN_API_URL`.
- TLS is the operator's responsibility — typically via a reverse proxy in front of the Docker container.

------

# Testing

## Manual

```bash
# install
go install github.com/ranaroussi/minigun/cli@latest

# sanity check
MINIGUN_API_URL=http://127.0.0.1:8080 minigun health

# run MCP inspector against the subcommand
npx -y @modelcontextprotocol/inspector minigun mcp
# → opens a UI at http://127.0.0.1:6274 where every tool, resource,
#   and prompt can be exercised by hand
```

## Automated

- `cli/internal/mcp/uri_test.go` — exhaustive coverage of resource URI parsing.
- `cli/internal/mcp/tools_test.go` — table-driven tests with a `httptest.Server` standing in for the MiniGun API; validates payload shape and error mapping.
- No tests are added against the official SDK's internals; the SDK is treated as a trusted dependency.

------

# Future work

- **Server-side MCP transport** (`/mcp` on the chi router) for scenarios where MiniGun runs colocated with the AI tooling, or for shared team usage.
- **`minigun mcp stdio-proxy`** — a tiny stdio↔HTTP bridge so the same binary can act as a shim for users who choose to run a remote `/mcp` endpoint without depending on `mcp-remote` from npm.
- **Per-tool dry-run mode.** A boolean `dry_run` field on `send_bulk` and `send_single` that returns the would-be Mailgun payload without delivering. Useful for letting the LLM rehearse a send.
- **Streaming progress as MCP notifications.** Emit `notifications/progress` from `send_bulk` as batches complete, so the LLM client can show real-time progress instead of polling `get_send_status`.
- **Config file fallback.** Read `~/.config/minigun/config.json` if neither flags nor env vars are present. Solves the macOS GUI-app env-var problem with no MCP client config required.

------

# Philosophy

MiniGun MCP is intentionally a thin wrapper.

```text
Keep the protocol layer dumb.
Keep the API the single source of truth.
Let the model do the model things.
```
