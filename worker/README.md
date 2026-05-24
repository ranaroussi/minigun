# MiniGun Worker

Cloudflare Worker port of [MiniGun](../README.md). TypeScript + Hono + D1 + Web Crypto, deployed via `wrangler`. Same HTTP API, same database schema, same HMAC token format as the Go server in [`../src/`](../src) — tokens signed by one verify on the other.

This is an alternate deployment target, not a replacement. Pick whichever fits your stack.

**See [../docs/cloudflare.md](../docs/cloudflare.md) for the full install, configuration, and operating walkthrough.**

## Quickref

```bash
cd worker
npm install
npx wrangler d1 create minigun
# paste database_id into wrangler.toml
npx wrangler d1 migrations apply minigun --remote
npx wrangler secret put MAILGUN_API_KEY
npx wrangler secret put MINIGUN_HMAC_SECRET
npx wrangler secret put MINIGUN_INTERNAL_SECRET
npx wrangler secret put MINIGUN_API_TOKEN
npx wrangler deploy
```

```bash
npx wrangler tail                       # stream production logs
npx wrangler deployments list           # version history
npx tsc --noEmit                        # typecheck
npx wrangler dev                        # local dev with a local D1
```
