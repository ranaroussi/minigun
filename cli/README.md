# MiniGun CLI

Standalone command-line client for [MiniGun](../README.md). Lives in its own Go module so it can be installed without pulling the server's SQLite, Mailgun, goose, and goldmark dependencies.

The same binary also speaks the [Model Context Protocol](https://modelcontextprotocol.io) over stdio (`minigun mcp`) so AI clients like Claude Desktop and Cursor can drive MiniGun in natural language.

**See [../docs/cli.md](../docs/cli.md) for the full command + MCP reference.**

## Install

```bash
go install github.com/ranaroussi/minigun/cli/cmd/minigun@latest
```

`go install` names the binary `minigun` (after the last path component). Make sure `$(go env GOBIN)` (or `$(go env GOPATH)/bin`) is on your `$PATH`.

Or build from a local checkout:

```bash
cd cli
go build -o minigun ./cmd/minigun
mv minigun /usr/local/bin/
```

## Quickref

```bash
export MINIGUN_API_URL=https://minigun.example.com
export MINIGUN_API_TOKEN=...

minigun health
minigun list create --name "Weekly" --slug newsletter
minigun contact add newsletter ran@example.com --params '{"first_name":"Ran"}'
minigun send bulk --list newsletter --subject "Hi" --from "Ran <r@x.com>" --md ./email.md
minigun send status s_xxxx --watch
minigun send stats  s_xxxx
```

```bash
# MCP mode for AI clients
minigun mcp
```
