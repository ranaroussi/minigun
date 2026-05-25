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

### `minigun send single`

```bash
minigun send single \
  --to ran@example.com \
  --subject "Hello" \
  --from "Ran <ran@example.com>" \
  --company acme \
  --md ./hello.md
```

Single transactional sends don't belong to a list, so `--company` is required: MiniGun resolves the sending domain from `company.sending_domain`. Pass `--domain` to override.

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

**Tools** — every MiniGun operation as an MCP tool. Destructive ones (`send_bulk`, `send_single`, `unsubscribe_contact`, `resume_send`) are tagged so MCP clients can render the appropriate confirmation UI.

| Tool | Maps to |
|---|---|
| `health` | `GET /healthz` |
| `create_list` | `POST /lists` |
| `add_contact` | `POST /lists/{list}/contacts` |
| `unsubscribe_contact` | `POST /lists/{list}/unsubscribe` |
| `send_single` | `POST /send/single` |
| `send_bulk` | `POST /send/bulk` |
| `resume_send` | `POST /send/{id}/resume` |
| `get_send_status` | `GET /send/{id}` |
| `get_send_stats` | `GET /send/{id}/stats` |

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
