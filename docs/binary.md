# Install: Go binary

Run MiniGun as a single static binary on any Linux / macOS host. No CGO, no JS, no runtime dependencies — just SQLite on disk.

## Build

```bash
git clone https://github.com/ranaroussi/minigun.git
cd minigun/src
go build -o minigun .
```

Cross-compile if you're building on one OS for another:

```bash
GOOS=linux  GOARCH=amd64 go build -o minigun-linux-amd64 ./src
GOOS=linux  GOARCH=arm64 go build -o minigun-linux-arm64 ./src
GOOS=darwin GOARCH=arm64 go build -o minigun-darwin-arm64 ./src
```

The binary is fully self-contained: the SQLite goose migrations, the unsubscribe / manage HTML templates, and the Mailgun client are all embedded.

## Run

```bash
export MAILGUN_API_KEY="key-..."
export MINIGUN_PUBLIC_URL="https://minigun.example.com"
export MINIGUN_HMAC_SECRET="$(openssl rand -hex 32)"
export MINIGUN_API_TOKEN="$(openssl rand -hex 32)"
export MINIGUN_DB_PATH="$PWD/minigun.db"

./minigun serve
```

The server listens on `:8080` by default (override via `MINIGUN_LISTEN_ADDR`).

On first boot it creates the SQLite file at `MINIGUN_DB_PATH`, runs the embedded goose migrations, and prints a warning if `MINIGUN_API_TOKEN` is unset (the API will be wide open). Set the token in production.

## systemd (Linux)

`/etc/systemd/system/minigun.service`:

```ini
[Unit]
Description=MiniGun email sender
After=network.target

[Service]
Type=simple
User=minigun
Group=minigun
WorkingDirectory=/var/lib/minigun
ExecStart=/usr/local/bin/minigun serve
Restart=on-failure
RestartSec=2s
EnvironmentFile=/etc/minigun.env

# Hardening
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/minigun

[Install]
WantedBy=multi-user.target
```

`/etc/minigun.env`:

```bash
MAILGUN_API_KEY=key-...
MAILGUN_REGION=us
MINIGUN_PUBLIC_URL=https://minigun.example.com
MINIGUN_HMAC_SECRET=...                 # openssl rand -hex 32
MINIGUN_API_TOKEN=...                   # openssl rand -hex 32
MINIGUN_DB_PATH=/var/lib/minigun/minigun.db
MINIGUN_LISTEN_ADDR=127.0.0.1:8080
```

```bash
sudo useradd --system --home /var/lib/minigun --shell /usr/sbin/nologin minigun
sudo install -m 0755 minigun /usr/local/bin/minigun
sudo install -m 0640 -o root -g minigun minigun.env /etc/minigun.env
sudo install -d -o minigun -g minigun /var/lib/minigun
sudo systemctl daemon-reload
sudo systemctl enable --now minigun
sudo journalctl -u minigun -f
```

Put nginx / Caddy / Cloudflare in front for TLS termination.

## Configuration

All configuration is via environment variables.

| Env var                        | Required | Default                  | Purpose |
|--------------------------------|----------|--------------------------|---------|
| `MAILGUN_API_KEY`              | yes      | —                        | Mailgun API key. Sent as HTTP Basic password (user is `api`). |
| `MAILGUN_REGION`               | no       | `us`                     | `us` or `eu`. Selects `https://api.mailgun.net` vs `https://api.eu.mailgun.net`. |
| `MAILGUN_API_BASE`             | no       | derived from region      | Explicit override for the API base URL. |
| `MINIGUN_PUBLIC_URL`           | yes      | —                        | Public origin used to build per-recipient unsubscribe URLs. |
| `MINIGUN_HMAC_SECRET`          | yes      | —                        | Secret used to HMAC-sign unsubscribe / manage tokens. Treat as sensitive. |
| `MINIGUN_API_TOKEN`            | no       | —                        | Bearer token required on every API request when set. `/healthz`, `/u/{token}`, and `/manage/{token}` stay public. If unset, the server runs open and prints a warning. |
| `MINIGUN_DB_PATH`              | no       | `/data/minigun.db`       | SQLite file path. |
| `MINIGUN_LISTEN_ADDR`          | no       | `:8080`                  | HTTP listen address. |
| `MINIGUN_TURNSTILE_SITE_KEY`   | no       | —                        | Cloudflare Turnstile site key. If unset, the two-step confirmation page is still shown — without a bot challenge. |
| `MINIGUN_TURNSTILE_SECRET_KEY` | no       | —                        | Turnstile secret. Required when site key is set. |
| `MAILGUN_WEBHOOK_SIGNING_KEY`  | no       | —                        | Mailgun HTTP webhook signing key. When set, `/webhooks/mailgun` accepts signed bounce/complaint events and auto-purges contacts. When unset, the endpoint refuses all requests. |
| `ENGAGEMENT_STATS_ENABLED`       | no       | `false`                  | Gates engagement retrieval — the events-pull cron behind the `/send/{id}/recipients`, `/send/{id}/clicks`, and `/contacts/{id}/engagement` read surface. Retrieval only; pruning is the separate `LIST_HYGIENE_AUTO_PRUNE_ENABLED`. Schema ships dormant. Deprecated alias: `EVENTS_ARCHIVE_ENABLED`. |
| `LIST_HYGIENE_AUTO_PRUNE_ENABLED` | no    | `false`                  | When `true`, the engagement-based prune executor runs once per day against every list. Manual `POST /lists/{list}/prune` works independently of this flag. |
| `LIST_HYGIENE_AUTO_PRUNE_BY_COUNT` | no   | `20`                     | Auto-prune contacts whose `messages_since_last_engagement >= N`. Set to `0` to disable. |
| `LIST_HYGIENE_AUTO_PRUNE_BY_RECENCY_DAYS` | no | `180`              | Auto-prune contacts whose last open/click is older than N days. Set to `0` to disable. |
| `LIST_HYGIENE_AUTO_PRUNE_NO_DELIVERY_DAYS` | no | `0` (disabled)    | Auto-prune contacts with no delivered events in N days. Defaults disabled. |

## Backups

The entire state of MiniGun is in `MINIGUN_DB_PATH`. To back up safely while the server is running:

```bash
sqlite3 /var/lib/minigun/minigun.db ".backup '/var/lib/minigun/minigun.db.backup'"
```

This uses SQLite's online backup API and is consistent without stopping the server.

## Upgrade

Stop the service, replace the binary, start the service. The embedded migrations run automatically on boot, so the schema stays in sync with the binary:

```bash
sudo systemctl stop minigun
sudo install -m 0755 minigun /usr/local/bin/minigun
sudo systemctl start minigun
```

## Health check

```bash
curl -fsS http://127.0.0.1:8080/healthz
# {"ok":true}
```

The endpoint pings SQLite and returns 200 only if the DB is reachable. Use it for systemd `ExecStartPre`, k8s probes, uptime monitors, etc.
