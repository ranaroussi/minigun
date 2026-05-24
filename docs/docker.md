# Install: Docker

Run MiniGun as a Docker container with a SQLite volume for state. The repo ships both a `Dockerfile` and a `docker-compose.yml` you can use straight from a checkout.

## Docker Compose (recommended)

```yaml
# docker-compose.yml
services:
  minigun:
    image: ghcr.io/ranaroussi/minigun:latest
    container_name: minigun
    restart: unless-stopped
    ports:
      - "8080:8080"
    volumes:
      - ./data:/data
    environment:
      MAILGUN_API_KEY: ${MAILGUN_API_KEY}
      MAILGUN_REGION: ${MAILGUN_REGION:-us}
      MINIGUN_PUBLIC_URL: ${MINIGUN_PUBLIC_URL}
      MINIGUN_HMAC_SECRET: ${MINIGUN_HMAC_SECRET}
      MINIGUN_API_TOKEN: ${MINIGUN_API_TOKEN}
      MINIGUN_TURNSTILE_SITE_KEY: ${MINIGUN_TURNSTILE_SITE_KEY:-}
      MINIGUN_TURNSTILE_SECRET_KEY: ${MINIGUN_TURNSTILE_SECRET_KEY:-}
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://127.0.0.1:8080/healthz"]
      interval: 30s
      timeout: 5s
      retries: 3
      start_period: 10s
```

`.env` (next to the compose file):

```bash
MAILGUN_API_KEY=key-...
MINIGUN_PUBLIC_URL=https://minigun.example.com
MINIGUN_HMAC_SECRET=...   # openssl rand -hex 32
MINIGUN_API_TOKEN=...     # openssl rand -hex 32
```

```bash
docker compose pull
docker compose up -d
docker compose logs -f minigun
```

## Plain `docker run`

```bash
docker run -d --name minigun --restart unless-stopped \
  -p 8080:8080 \
  -v "$PWD/data:/data" \
  -e MAILGUN_API_KEY=key-... \
  -e MAILGUN_REGION=us \
  -e MINIGUN_PUBLIC_URL=https://minigun.example.com \
  -e MINIGUN_HMAC_SECRET="$(openssl rand -hex 32)" \
  -e MINIGUN_API_TOKEN="$(openssl rand -hex 32)" \
  ghcr.io/ranaroussi/minigun:latest
```

The container mounts `./data` to `/data` inside the container — that's where SQLite lives. **Persist this volume** or you'll lose every send history, contact, and unsubscribe event on restart.

## Build the image yourself

```bash
git clone https://github.com/ranaroussi/minigun.git
cd minigun
docker build -t minigun .
docker run -d --name minigun -p 8080:8080 -v "$PWD/data:/data" \
  -e MAILGUN_API_KEY=key-... \
  -e MINIGUN_PUBLIC_URL=http://localhost:8080 \
  -e MINIGUN_HMAC_SECRET="$(openssl rand -hex 32)" \
  -e MINIGUN_API_TOKEN="$(openssl rand -hex 32)" \
  minigun
```

The Dockerfile is a multi-stage Alpine build. The final image is roughly 15 MB and contains only the static `minigun` binary plus CA certificates.

## Configuration

| Env var                        | Required | Default                  | Purpose |
|--------------------------------|----------|--------------------------|---------|
| `MAILGUN_API_KEY`              | yes      | —                        | Mailgun API key. Sent as HTTP Basic password (user is `api`). |
| `MAILGUN_DOMAIN`               | no       | derived from each send's `from` | Sending domain. If unset, MiniGun parses the host part of the `from` header. Set this only to override (e.g., display From on `acme.com`, send through `mg.acme.com`). |
| `MAILGUN_REGION`               | no       | `us`                     | `us` or `eu`. Selects `https://api.mailgun.net` vs `https://api.eu.mailgun.net`. |
| `MAILGUN_API_BASE`             | no       | derived from region      | Explicit override for the API base URL. |
| `MINIGUN_PUBLIC_URL`           | yes      | —                        | Public origin used to build per-recipient unsubscribe URLs. |
| `MINIGUN_HMAC_SECRET`          | yes      | —                        | Secret used to HMAC-sign unsubscribe / manage tokens. |
| `MINIGUN_API_TOKEN`            | no       | —                        | Bearer token required on every API request when set. `/healthz`, `/u/{token}`, and `/manage/{token}` stay public. If unset, the server runs open and prints a warning. |
| `MINIGUN_DB_PATH`              | no       | `/data/minigun.db`       | SQLite file path inside the container. |
| `MINIGUN_LISTEN_ADDR`          | no       | `:8080`                  | HTTP listen address. |
| `MINIGUN_TURNSTILE_SITE_KEY`   | no       | —                        | Cloudflare Turnstile site key. |
| `MINIGUN_TURNSTILE_SECRET_KEY` | no       | —                        | Turnstile secret. Required when site key is set. |

## Reverse-proxy / TLS

Don't expose port 8080 directly to the internet. Put nginx, Caddy, or Cloudflare in front:

```nginx
server {
  listen 443 ssl http2;
  server_name minigun.example.com;

  ssl_certificate     /etc/letsencrypt/live/minigun.example.com/fullchain.pem;
  ssl_certificate_key /etc/letsencrypt/live/minigun.example.com/privkey.pem;

  location / {
    proxy_pass http://127.0.0.1:8080;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
  }
}
```

Caddy is even simpler:

```caddy
minigun.example.com {
  reverse_proxy localhost:8080
}
```

## Backups

The container's `/data` directory holds the SQLite database. Back it up safely while the container is running:

```bash
docker exec minigun sqlite3 /data/minigun.db ".backup '/data/minigun.db.backup'"
# then copy minigun.db.backup off the host
```

This uses SQLite's online backup API — safe to run on a live DB.

## Upgrade

```bash
docker compose pull
docker compose up -d
```

The embedded goose migrations run automatically on boot, so the schema stays in sync with the image tag.

## Health check

The Compose service has a built-in `healthcheck`. To probe manually:

```bash
curl -fsS http://127.0.0.1:8080/healthz
# {"ok":true}
```
