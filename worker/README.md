# MiniGun — Cloudflare Worker

The same MiniGun server, ported to a Cloudflare Worker. TypeScript + Hono + D1 + Web Crypto, deployed via `wrangler`. Same HTTP API, same database schema, same HMAC token format as the Go server in [`../src/`](../src) — tokens signed by one verify on the other.

This is an alternate deployment target, not a replacement. Pick whichever fits your stack: a Go binary on a VM/container, or a Worker on Cloudflare's edge.

## When to pick the Worker

- You already run on Cloudflare and want zero new infrastructure.
- You want the bulk-send loop to survive crashes without you running a process at all (cron + self-call resume handles it).
- You want D1's per-region replicas instead of a single SQLite file.

## When to pick the Go server

- You need single-binary on-prem.
- You want a real long-running process with goroutines for the bulk send loop.
- You don't want Cloudflare in the critical path.

## Quick deploy

Prerequisites: a Cloudflare account, the Wrangler CLI auth'd via `CLOUDFLARE_API_TOKEN` (see permission matrix below).

```bash
cd worker
npm install

# Create the D1 database (one-time)
wrangler d1 create minigun
# Paste the printed database_id into wrangler.toml

# Apply migrations to the remote D1
wrangler d1 migrations apply minigun --remote

# Set secrets
wrangler secret put MAILGUN_API_KEY
wrangler secret put MINIGUN_HMAC_SECRET       # openssl rand -hex 32
wrangler secret put MINIGUN_INTERNAL_SECRET   # openssl rand -hex 32
wrangler secret put MINIGUN_API_TOKEN         # openssl rand -hex 32 (gates the public API)

# Optional secrets
wrangler secret put MAILGUN_DOMAIN            # only if you want to override the From-derived domain

# Deploy
wrangler deploy
```

If you bind to a custom domain (recommended), update the `routes` block in `wrangler.toml` first.

## Configuration

### `[vars]` in `wrangler.toml`

| Var                  | Required | Default                            | Purpose |
|----------------------|----------|------------------------------------|---------|
| `MINIGUN_PUBLIC_URL` | yes      | —                                  | Public origin used to build unsubscribe / manage URLs (e.g., `https://mailer.example.com`). |
| `MAILGUN_REGION`     | no       | `us`                               | `us` or `eu`. Selects the Mailgun API base. |
| `MAILGUN_API_BASE`   | no       | derived from region                | Explicit override for the API base URL. |
| `REDIRECT_URL`       | no       | —                                  | Where to 302-redirect visitors who hit `GET /` in a browser. Leave unset for a plain 404. |

### Secrets (`wrangler secret put <NAME>`)

| Secret                    | Required | Purpose |
|---------------------------|----------|---------|
| `MAILGUN_API_KEY`         | yes      | Mailgun API key. Sent as HTTP Basic password. |
| `MAILGUN_DOMAIN`          | no       | Sending domain. If unset, the worker parses the host part of each send's `from` header. Set only to override (e.g., display From on `acme.com`, send through `mg.acme.com`). |
| `MINIGUN_HMAC_SECRET`     | yes      | HMAC key for unsubscribe / manage tokens. Must match the Go server if you ever want to cross-verify. |
| `MINIGUN_API_TOKEN`       | yes      | Bearer token required on every API request. The `/`, `/healthz`, `/u/`, and `/manage/` routes stay public. |
| `MINIGUN_INTERNAL_SECRET` | yes      | Implementation detail — gates the worker's self-fetches to `/send/:id/next`. The chain and cron use it; operators never need to know it. |
| `MINIGUN_TURNSTILE_SITE_KEY` | no    | Cloudflare Turnstile site key for the unsubscribe page. |
| `MINIGUN_TURNSTILE_SECRET_KEY` | no  | Turnstile secret. Required when site key is set. |

### Cloudflare API token permissions

A custom token with these scopes is enough to deploy:

| Section | Permission | Access |
|---------|-----------|--------|
| Account | Workers Scripts | Edit |
| Account | D1 | Edit |
| Account | Workers KV Storage | Edit (wrangler quirk) |
| Account | Workers Tail | Read |
| Account | Account Settings | Read |
| User    | User Details | Read |
| User    | Memberships | Read |

Add `Zone → Workers Routes → Edit` + `Zone → DNS → Edit` only if you bind to a custom domain on a Cloudflare zone.

### One-time account setup (cron triggers only)

Cloudflare requires the account's `workers.dev` subdomain to be claimed before cron triggers can register, **even when you don't deploy to workers.dev.** Visit https://dash.cloudflare.com/<account-id>/workers/onboarding once and pick any subdomain — this only satisfies the API, it doesn't expose your worker on `workers.dev` (we set `workers_dev = false`).

If you'd rather skip this step, comment out the `[triggers]` block in `wrangler.toml`. The bulk-send chain still self-perpetuates; you lose only the recovery sweeper.

## Architecture notes

### Bulk send loop

Each step is one HTTP request:

```
POST /send/bulk → create Send row → kick first step (POST /send/:id/next)
                                       │
                                       ▼
           ┌───────────────────────────────────────────────┐
           │  /send/:id/next handler                       │
           │    1. atomic batch claim (in_flight=true)     │
           │    2. render + sign tokens                    │
           │    3. POST to Mailgun                         │
           │    4. mark batch done; advance cursor         │
           │    5. ctx.waitUntil(sleep(throttle) + fetch)  │ ─┐
           └───────────────────────────────────────────────┘  │
                                                              │ self-call
                                       ┌──────────────────────┘
                                       ▼
                                 (loop until done)
```

`POST /send/:id/next` and `POST /send/:id/resume` are aliases — same handler. The first is the chain's natural verb, the second is the operator's. Both accept either the bearer `MINIGUN_API_TOKEN` or the internal `x-internal-secret` header.

### Cron watchdog (`* * * * *`)

Every minute, looks for sends in `status='running'` with `updated_at < now() - 2min` and pokes them with a `POST /send/:id/next`. The atomic batch claim makes a double-call safe: if a batch is genuinely still in flight, `step()` returns `{state: 'busy'}` and no extra work happens.

### Auto-injected unsubscribe footer

If your email content has neither `{{unsubscribe}}` nor `{{unsub_url}}`, MiniGun appends:

- HTML: `<p>&nbsp;<br><a href="{{unsubscribe}}">Unsubscribe</a></p>`
- Text:  `Unsubscribe:\n{{unsubscribe}}`

Both placeholders resolve to the same HMAC-signed `/u/{token}` URL — Mailgun substitutes them server-side via the `recipient-variables` map. The `List-Unsubscribe` and `List-Unsubscribe-Post: List-Unsubscribe=One-Click` headers (RFC 8058) are always set on bulk sends.

## Operating

```bash
wrangler tail                       # stream production logs
wrangler d1 execute minigun --remote --command 'SELECT * FROM sends ORDER BY created_at DESC LIMIT 10'
wrangler secret list                # see which secrets are bound
wrangler deployments list           # version history
```

To resume a stuck send manually:

```bash
curl -X POST -H "Authorization: Bearer $MINIGUN_API_TOKEN" \
  https://mailer.example.com/send/snd_XXXX/resume
```

The same call also works against `POST /send/snd_XXXX/next`.

## Development

```bash
cd worker
npm install
npx tsc --noEmit                    # typecheck
npx wrangler dev                    # local dev with a local D1
npx vitest                          # unit tests (when present)
```

## Files

```
worker/
├── README.md
├── package.json
├── tsconfig.json
├── vitest.config.ts
├── wrangler.toml
├── migrations/
│   ├── 0001_init.sql
│   └── 0002_companies.sql
└── src/
    ├── index.ts          # Hono app, middleware, cron handler, GET / redirect
    ├── env.ts            # Env type + helpers
    ├── lib/              # ids, token (Web Crypto HMAC), pagination, slug, mailgun, markdown
    ├── store/            # D1 repository layer
    ├── routes/           # auth, health, companies, lists, contacts, sends, unsubscribe, manage
    ├── send/             # render, single, bulk (with scheduleNextStep), cron
    └── pages/            # JSX templates for /u/ and /manage/
```
