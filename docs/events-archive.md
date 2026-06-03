# Per-send event archive & engagement rollups

Backing the [engagement-based prune](./list-hygiene.md#proactive-hygiene--engagement-based-prune) is a local rollup of Mailgun events. Once you set `ENGAGEMENT_STATS_ENABLED=true`, MiniGun pulls Mailgun's events API on a burst-then-daily schedule (`+0`, `+1h`, `+6h`, `+24h` after a send, then daily for 30 days). Each pull begins at the highest event timestamp the previous pull saw (the send's `created_at` on the first pull) and folds every event straight into two bounded engagement tiers — **no raw per-event rows are stored**.

The archive is **opt-in.** Default-off `ENGAGEMENT_STATS_ENABLED` means the schema lands dormant; flip the flag whenever you're ready to start collecting.

## The two rollup tiers

- **`contact_message_engagement`** — one row per `(send, contact)`: `sent_at`, `delivered_at`, `first/last_open_at` + `total_opens`, `first/last_click_at` + `total_clicks`, plus `failed`/`complained_at`/`unsubscribed_at`. The per-message detail tier (timestamps are epoch seconds). Bounded to ≤ recipients per send. A row requires a resolvable `contact_id`, so a **list-less transactional single only appears here if its recipient already exists as a contact** — a one-off send to a brand-new address is not back-filled into `contacts` and won't surface in the recipient rollup.
- **`contact_engagement`** — the per-`(contact, list)` lifetime rollup (`total_delivered`, `total_opens`, `total_clicks`, `messages_since_last_engagement`, `last_engagement_at_ms`). This is what the prune query reads against.

Because a single contact can open or click many times, a raw event log would grow without bound — so MiniGun keeps no such table. The two rollups together hold at most one row per recipient (per send / per list), and that's all the prune logic and the read surface need.

## Per-URL click rollup

A third rollup, **`contact_message_clicks`**, records clicks at the per-link grain — one row per `(send, contact, url)` with `first/last_click_at` + a click count. URLs are stored canonical (scheme+host lowercased, query string and fragment stripped) so a destination aggregates regardless of tracking params or per-recipient tokens. It's the data behind audience segmentation ("who clicked this link"); `SUM(total_clicks)` over a `(send, contact)` equals that pair's `contact_message_engagement.total_clicks`.

## Read endpoints

```bash
# Per-recipient message engagement for a send (one row per contact):
minigun send recipients s_xxxx --all > recipients.jsonl

# Per-URL clicks for a send (one row per contact + clicked link):
minigun send clicks s_xxxx --all > clicks.jsonl

# Lifetime engagement summary for a contact (per-list or global):
minigun contact engagement alice@example.com
minigun contact engagement alice@example.com --list newsletter
```

These surfaces are available across HTTP, MCP (`list_send_recipients`, `list_send_clicks`, `get_contact_engagement` — all ReadOnly), and the 4 SDKs.

## Operational properties

- **Incremental, no duplicates.** Each pull's window begins strictly after the previous pull's highest event timestamp, so an event is seen once and the per-call counter increments stay correct without any dedup ledger.
- **Out-of-order safe within a pull.** Timestamp fields converge via `MIN`/`MAX`; a late `delivered` for an already-opened message doesn't inflate dormancy (guarded by a `CASE WHEN excluded.last_delivered_at_ms > last_engagement_at_ms` predicate).
- **Bounded per tick.** 50 pages × 300 events = 15k events/send max; when the cap is hit with more pages, the watermark advances to the last-processed event timestamp so the next tick continues from there.
- **Freezes after 30 days.** Sets `events_archive_complete = 1` so the cron stops polling; the rollups remain queryable forever.
