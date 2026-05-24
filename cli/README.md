# MiniGun CLI

A small, standalone command-line client for [MiniGun](../README.md). Lives in its own Go module so it can be installed on your laptop without pulling the server's SQLite, Mailgun, goose, and goldmark dependencies.

## Install

```bash
go install github.com/ranaroussi/minigun/cli@latest
```

Or build from a local checkout:

```bash
cd cli
go build -o minigun .
mv minigun /usr/local/bin/  # or anywhere on PATH
```

The binary is named `minigun`. If you also run the server binary on the same machine, alias one of them.

## Configuration

| Flag        | Env var              | Default                  |
|-------------|----------------------|--------------------------|
| `--api`     | `MINIGUN_API_URL`    | `http://127.0.0.1:8080`  |
| `--token`   | `MINIGUN_API_TOKEN`  | _(unset)_                |

`MINIGUN_API_TOKEN`, if set, is sent as `Authorization: Bearer <token>` on every request. The server requires this header on all endpoints when `MINIGUN_API_TOKEN` is set on the server side. The `/u/{token}` and `/manage/{token}` HTML routes are exempt (they carry their own HMAC token in the URL), as is `/healthz` for container probes.

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

### `minigun list create`

```bash
minigun list create --name "Weekly Newsletter" --slug newsletter
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
  --md ./hello.md
```

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

Aggregate stats (Mailgun Metrics API + local unsubscribe count):

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

### `minigun mcp`

Run an MCP (Model Context Protocol) server over stdio so AI clients (Claude Desktop, Cursor, Zed, Continue, Goose, etc.) can drive MiniGun in natural language. The MCP server reuses the same `--api`/`--token` configuration as the CLI.

```bash
# point at your server (or rely on MINIGUN_API_URL)
export MINIGUN_API_URL=https://minigun.example.com
export MINIGUN_API_TOKEN=...

# sanity check
minigun mcp < /dev/null  # exits immediately on EOF

# or, in a Claude Desktop / Cursor MCP config:
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

The `env` block is optional if your MCP client inherits the shell environment. macOS GUI applications generally do **not**, so prefer the explicit form there.

Tools exposed: `health`, `create_list`, `add_contact`, `unsubscribe_contact`, `send_single`, `send_bulk`, `resume_send`, `get_send_status`, `get_send_stats`. Destructive tools are tagged so MCP clients can render the appropriate confirmation UI.

Resources exposed: `minigun://lists`, `minigun://lists/{slug}`, `minigun://lists/{slug}/contacts`, `minigun://sends`, `minigun://sends/{id}`, `minigun://sends/{id}/stats`. Paginated resources accept `?cursor=` and `?limit=` query parameters appended to the URI.

Prompts exposed: `compose_newsletter` (drafts a Markdown newsletter using MiniGun's variable conventions) and `audit_send` (produces a post-send report with delivery, open, click, complaint, and unsubscribe rates).

To validate locally with the [MCP inspector](https://github.com/modelcontextprotocol/inspector):

```bash
npx -y @modelcontextprotocol/inspector minigun mcp
```

## Examples

End-to-end mini-walkthrough:

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
