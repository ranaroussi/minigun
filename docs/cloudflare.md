# Install: Cloudflare Worker

MiniGun ships with a full TypeScript port that runs on Cloudflare Workers + D1. Same HTTP API, same database schema, same HMAC token format as the Go server — tokens signed by one verify on the other. The source lives in [`worker/`](../worker/).

This is an alternate deployment target, not a replacement. Pick whichever fits your stack: a Go binary on a VM/container, or a Worker on Cloudflare's edge.

## When to pick the Worker

- You already run on Cloudflare and want zero new infrastructure.
- You want the bulk-send loop to survive crashes without you running a process at all (cron + self-call resume handles it).
- You want D1's per-region replicas instead of a single SQLite file.

## When to pick the Go binary instead

- You need single-binary on-prem.
- You want a real long-running process with goroutines for the bulk send loop.
- You don't want Cloudflare in the critical path.

See [docs/binary.md](./binary.md) and [docs/docker.md](./docker.md) for those.

## Quick deploy

Prerequisites: a Cloudflare account, the Wrangler CLI auth'd via `CLOUDFLARE_API_TOKEN` (see permission matrix below).

```bash
cd worker
npm install

# Create the D1 database (one-time)
npx wrangler d1 create minigun
# Paste the printed database_id into wrangler.toml under [[d1_databases]]

# Apply migrations to the remote D1
npx wrangler d1 migrations apply minigun --remote

# Set secrets
npx wrangler secret put MAILGUN_API_KEY
npx wrangler secret put MINIGUN_HMAC_SECRET       # openssl rand -hex 32
npx wrangler secret put MINIGUN_INTERNAL_SECRET   # openssl rand -hex 32
npx wrangler secret put MINIGUN_API_TOKEN         # openssl rand -hex 32 (gates the public API)

# Deploy
npx wrangler deploy
```

If you bind to a custom domain (recommended), update the `routes` block in `worker/wrangler.toml` first.

## Configuration

### `[vars]` in `wrangler.toml`

| Var                  | Required | Default                            | Purpose |
|----------------------|----------|------------------------------------|---------|
| `MINIGUN_PUBLIC_URL` | yes      | —                                  | Public origin used to build unsubscribe / manage URLs (e.g., `https://mailer.example.com`). |
| `MAILGUN_REGION`     | no       | `us`                               | `us` or `eu`. Selects the Mailgun API base. |
| `MAILGUN_API_BASE`   | no       | derived from region                | Explicit override for the API base URL. |
| `REDIRECT_URL`       | no       | —                                  | Where to 302-redirect visitors who hit `GET /` in a browser. Leave unset for a plain 404. |
| `EVENTS_ARCHIVE_ENABLED` | no   | `false`                            | Activates the Mailgun events archive pull cron + the read surface (`/send/{id}/events`, `/contacts/{id}/engagement`). Schema lands dormant; flip to `true` to start collecting. |
| `LIST_HYGIENE_AUTO_PRUNE_ENABLED` | no | `false`                  | When `true`, the engagement-based prune executor runs once per day against every list. Manual `POST /lists/{list}/prune` works independently of this flag. Persistent daily throttle via the `worker_state` D1 table — re-deploys can't double-fire. |
| `LIST_HYGIENE_AUTO_PRUNE_BY_COUNT` | no | `20`                    | Auto-prune contacts whose `messages_since_last_engagement >= N`. Set to `0` to disable this criterion in the cron. |
| `LIST_HYGIENE_AUTO_PRUNE_BY_RECENCY_DAYS` | no | `180`            | Auto-prune contacts whose last open/click is older than N days. Set to `0` to disable. |
| `LIST_HYGIENE_AUTO_PRUNE_NO_DELIVERY_DAYS` | no | `0` (disabled)  | Auto-prune contacts subscribed before the cutoff with no delivered events in N days. Aggressive on new lists — defaults off. |

### Secrets (`wrangler secret put <NAME>`)

| Secret                    | Required | Purpose |
|---------------------------|----------|---------|
| `MAILGUN_API_KEY`         | yes      | Mailgun API key. Sent as HTTP Basic password. |
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

Cloudflare requires the account's `workers.dev` subdomain to be claimed before cron triggers can register, **even when you don't deploy to workers.dev.** Visit `https://dash.cloudflare.com/<account-id>/workers/onboarding` once and pick any subdomain — this only satisfies the API, it doesn't expose your worker on `workers.dev` (we set `workers_dev = false`).

If you'd rather skip this step, comment out the `[triggers]` block in `wrangler.toml`. The bulk-send chain still self-perpetuates; you lose only the recovery sweeper and the post-send stats refresher.

## Architecture notes

### Bulk send loop

Each step is one HTTP request:

```
POST /send/bulk → create Send row → run batch #1 inline → schedule batch #2 in ctx.waitUntil
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

The first batch runs inside the original `POST /send/bulk` request before responding `202`, so a single-batch send is always finished by the time the operator sees the response. Only batches `#2..N` ride the self-call chain. This eliminates a class of "send is stuck in `queued`" failures we used to see when the initial fire-and-forget self-fetch got dropped at the edge.

`POST /send/:id/next` and `POST /send/:id/resume` are aliases — same handler. The first is the chain's natural verb, the second is the operator's. Both accept either the bearer `MINIGUN_API_TOKEN` or the internal `x-internal-secret` header.

### Cron watchdog (`* * * * *`)

Every minute the worker:

1. Looks for sends in `status IN ('queued', 'running')` with `updated_at < now() - 2min` and pokes them with `POST /send/:id/next`. The atomic batch claim makes a double-call safe. `queued` is included because a worker crash before batch #1 finishes can leave a brand-new send sitting at `queued` forever otherwise.
2. Walks the `send_stats` table for any send whose `next_fetch_at` is due, pulls fresh per-send aggregates from Mailgun's Metrics API, and persists them. Mailgun retains event logs for only 5 days; this keeps stats permanent in your D1.
3. If `EVENTS_ARCHIVE_ENABLED=true`, replays any `mailgun_events` rows whose engagement summary update was skipped in a prior tick (insert succeeded but the UPSERT failed), then walks the events-pull schedule for any send whose next beat is due. Schedule is burst-then-daily (`+0`, `+1h`, `+6h`, `+24h`, then daily for 30 days). No-op when the flag is off.
4. If `LIST_HYGIENE_AUTO_PRUNE_ENABLED=true`, runs the engagement-based prune executor against every list using the configured thresholds. Persistent daily throttle via `worker_state.auto_prune_last_run_ms` — even though the cron itself fires every minute, the prune body refuses to re-run within 24h. No-op when the flag is off.

### Auto-injected unsubscribe footer

If your email content has neither `{{unsubscribe}}` nor `{{unsub_url}}`, MiniGun appends:

- HTML: `<p>&nbsp;<br><a href="{{unsubscribe}}">Unsubscribe</a></p>`
- Text:  `Unsubscribe:\n{{unsubscribe}}`

Both placeholders resolve to the same HMAC-signed `/u/{token}` URL — Mailgun substitutes them server-side via the `recipient-variables` map. The `List-Unsubscribe` and `List-Unsubscribe-Post: List-Unsubscribe=One-Click` headers (RFC 8058) are always set on bulk sends.

## Operating

```bash
npx wrangler tail                       # stream production logs
npx wrangler d1 execute minigun --remote --command 'SELECT * FROM sends ORDER BY created_at DESC LIMIT 10'
npx wrangler secret list                # see which secrets are bound
npx wrangler deployments list           # version history
```

To resume a stuck send manually:

```bash
curl -X POST -H "Authorization: Bearer $MINIGUN_API_TOKEN" \
  https://mailer.example.com/send/snd_XXXX/resume
```

The same call also works against `POST /send/snd_XXXX/next`.

## Local development

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
├── package.json
├── tsconfig.json
├── vitest.config.ts
├── wrangler.toml
├── migrations/
│   ├── 0001_init.sql
│   ├── 0002_companies.sql
│   ├── 0003_send_stats.sql
│   ├── 0004_sending_domain.sql
│   ├── 0005_test_mode.sql
│   ├── 0006_complaint_events.sql
│   ├── 0007_events_archive.sql
│   ├── 0008_unsub_reason.sql
│   └── 0009_phase5.sql
└── src/
    ├── index.ts          # Hono app, middleware, cron handler, GET / redirect
    ├── env.ts            # Env type + helpers
    ├── lib/              # ids, token (Web Crypto HMAC), pagination, slug, mailgun, markdown
    ├── store/            # D1 repository layer
    ├── routes/           # auth, health, companies, lists, contacts, sends, unsubscribe, manage
    ├── send/             # render, single, bulk (with scheduleNextStep), cron, stats refresher
    └── pages/            # JSX templates for /u/ and /manage/
```
