# Automatic list hygiene

The single biggest reason newsletter senders trash their reputation is mailing addresses that no longer exist (hard bounces), actively flagged the previous send as spam, or keep receiving messages they never engage with. MiniGun handles all three automatically with two complementary mechanisms:

1. **Reactive hygiene** — a Mailgun-signed webhook auto-purges hard bounces and spam complaints in real time.
2. **Proactive hygiene** — a per-`(contact, list)` engagement archive lets you (or an opt-in daily cron) unsubscribe dormant contacts on three configurable signals.

Proactive hygiene is powered by the [per-send event archive](./events-archive.md).

---

## Reactive hygiene — `POST /webhooks/mailgun`

A Mailgun webhook endpoint that listens for the two events that matter:

| Event | Action |
|---|---|
| `failed` + `severity=permanent` (hard bounce) | Contact + every subscription on every list, **purged**. |
| `complained` (spam complaint / FBL) | Contact + every subscription **purged**, AND a permanent row is written to `complaint_events` so the address can be filtered out of future CSV imports. |
| `failed` + `severity=temporary` (soft bounce) | No-op. Mailgun is already retrying on the SMTP side. |
| `delivered`, `opened`, `clicked`, anything else | No-op 200. |

Every payload is HMAC-verified before we look at the body. The scheme matches Mailgun's reference exactly:

```
expected = hex(HMAC_SHA256(signing_key, timestamp + token))
accept   = constant-time-equal(expected, payload.signature) ∧ |now - timestamp| < 15min
```

Without `MAILGUN_WEBHOOK_SIGNING_KEY` configured, the endpoint **fails closed**: every request is `401`. So setup is one secret + a registration in Mailgun's dashboard (Sending → Webhooks):

```bash
# Worker:
echo 'paste-mailgun-http-webhook-signing-key-here' \
  | wrangler secret put MAILGUN_WEBHOOK_SIGNING_KEY

# Binary:
export MAILGUN_WEBHOOK_SIGNING_KEY=...
```

Then register `https://your-domain/webhooks/mailgun` for "Permanent Failure" and "Spam Complaints" on each sending domain.

Idempotent at every layer:
- `complaint_events.mailgun_event_id` is `UNIQUE` + `INSERT OR IGNORE` → webhook retries deduplicate at the storage layer.
- `DeleteContact` returns 200 `already-gone` (not 500) when the contact's already been purged by a prior delivery → Mailgun stops retrying instead of looping for 8 hours.
- HMAC verification is constant-time. Timestamps in the future are rejected as aggressively as stale ones.

### Hard-purge as a first-class operation

The same hard-purge that the webhook performs is also available as a first-class operation across all three surfaces, for when you need to script bounce cleanup yourself (e.g. importing a list of hard bounces from a previous provider, or pruning a stale segment by hand):

```bash
# HTTP:
curl -X DELETE -H "Authorization: Bearer $MINIGUN_API_TOKEN" \
     https://your-domain/contacts/bounced@example.com
# or:
curl -X DELETE -H "Authorization: Bearer $MINIGUN_API_TOKEN" \
     https://your-domain/contacts/c_PP5AA3MBXS

# CLI:
minigun contact delete bounced@example.com
minigun contact delete c_PP5AA3MBXS

# MCP (from any client — Claude, Cursor, Zed, etc.):
#   tool: delete_contact
#   args: { "id_or_email": "bounced@example.com" }
```

The endpoint accepts either the contact id (`c_*`) or the email address, and returns the deleted contact + how many subscriptions were removed. Same semantics as the webhook path — full purge of the contact + all subscriptions + unsubscribe-event audit rows in a single transaction.

Distinct from `contact unsubscribe`, which preserves the subscription row with `subscribed=0` (correct for user-initiated opt-outs — you want to remember they opted out so a future re-import doesn't silently re-subscribe them).

---

## Proactive hygiene — engagement-based prune

Hard bounces handle "the address doesn't exist." Complaints handle "this is spam." Engagement-based prune handles the middle ground: addresses that *do* exist, *don't* complain, but ignore everything you send. They cost you reputation faster than the first two combined, because mailbox providers measure engagement, and a low-engagement sender lands in the Promotions tab — or worse.

`POST /lists/{list}/prune` is the operator surface for cleaning that cohort. Three OR'd criteria, any combination, **`dry_run=true` by default**:

| Criterion | What it matches |
|---|---|
| `min_messages_since_engagement` | Contacts who received ≥N delivered messages with no open/click since (prune-by-count). |
| `dormant_for_days` | Contacts whose last open/click is older than D days (prune-by-recency). |
| `no_delivery_for_days` | Contacts subscribed before the cutoff with no delivered events in the last D days (prune-by-no-delivery — useful for never-engaged cohorts where Mailgun is rejecting at the gateway). |

Run a dry-run first to see the candidate set, then re-run with `--apply` (CLI) or `dry_run: false` (HTTP/SDK) to commit:

```bash
# Dry-run — defaults to dry, returns candidates + sample + reason_counts.
minigun list prune weekly --by-count 20 --by-recency 180 --no-delivery-for 90

# Commit — writes one unsubscribe_events audit row per pruned contact
# with the most specific matched reason (count > recency > no-delivery).
minigun list prune weekly --by-count 20 --by-recency 180 --apply
```

Same surface across HTTP, MCP, and all 4 SDKs:

```bash
# HTTP:
curl -X POST -H "Authorization: Bearer $MINIGUN_API_TOKEN" \
     -H "Content-Type: application/json" \
     -d '{"min_messages_since_engagement":20,"dormant_for_days":180,"dry_run":false}' \
     https://your-domain/lists/weekly/prune

# MCP:
#   tool: prune_list
#   args: { "list": "weekly", "min_messages_since_engagement": 20, "dry_run": false }
```

Audit-row reason precedence is **count > recency > no-delivery** — the most actionable signal wins when a contact matches multiple criteria.

Each call is bounded (`limit` defaults to 1000, max 10000). Massive backlogs drain over multiple invocations so anomalies surface in the audit log before you've unsubscribed half the list.

### Opt-in daily auto-prune cron

Set `LIST_HYGIENE_AUTO_PRUNE_ENABLED=true` and at least one threshold env var (defaults: 20 wasted deliveries, 180 days dormant, no-delivery off), and the same prune executor runs once per day against every list. Conservative defaults; persistent daily throttle via `worker_state` so a Worker re-deploy or Go crash-loop can't double-fire.

| Env var | Default | Purpose |
|---|---|---|
| `LIST_HYGIENE_AUTO_PRUNE_ENABLED` | `false` | Master switch for the daily cron. Manual `POST /lists/{list}/prune` works independently of it. |
| `LIST_HYGIENE_AUTO_PRUNE_BY_COUNT` | `20` | Auto-prune contacts whose `messages_since_last_engagement >= N`. `0` disables this criterion. |
| `LIST_HYGIENE_AUTO_PRUNE_BY_RECENCY_DAYS` | `180` | Auto-prune contacts whose last open/click is older than N days. `0` disables. |
| `LIST_HYGIENE_AUTO_PRUNE_NO_DELIVERY_DAYS` | `0` (disabled) | Auto-prune contacts subscribed before the cutoff with no delivered events in N days. Aggressive on new lists — defaults disabled. |
