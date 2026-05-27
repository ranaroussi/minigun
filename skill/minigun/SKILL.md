---
name: minigun
description: |
  Operate MiniGun (https://github.com/ranaroussi/minigun) — the self-hosted Mailgun-backed email sender — for any email-related task. Use this skill whenever the user wants to: send a newsletter or marketing campaign, send a transactional / one-off email, manage a mailing list, add or remove contacts, check on a previous send, debug a stuck or failing send, clean bounced addresses, set up IP warming, audit deliverability, or run any kind of email-sending workflow. Also use this skill when the user mentions MiniGun by name, types or stares at a `minigun ...` CLI command, opens a markdown file that looks like newsletter copy, or asks anything about Mailgun deliverability, bounce handling, spam complaints, List-Unsubscribe headers, or DMARC. Use this skill even when the user doesn't explicitly say "MiniGun" — if the task is "send email to my list" or "draft a newsletter" or "why didn't subscribers get my email", this is the skill. Prefer the MCP tools when an MCP connection is available (every destructive op renders a confirmation prompt); fall back to the `minigun` CLI; fall back to raw HTTP only as a last resort.
---

# MiniGun operator skill

This skill teaches you to run [MiniGun](https://github.com/ranaroussi/minigun) end-to-end: drafting copy, dispatching campaigns, managing lists, and keeping deliverability healthy. The user wants you to act as a senior email-operations engineer, not as a CLI parrot — pick the right interface for the moment, surface the right checks before a destructive op, and explain *why* you're doing what you're doing.

## What MiniGun is, in one paragraph

MiniGun is a thin layer over Mailgun. Mailgun handles delivery, tracking, and reputation; MiniGun owns contacts, lists, per-recipient unsubscribe links, crash-safe bulk sends, and per-send stats persistence. It exposes one HTTP API (`http://...` or `https://mailer.<domain>`) plus a `minigun` CLI plus an MCP server that wraps the CLI as tools. Templates are written in Markdown with `{{first_name | "there"}}`-style variable placeholders. Bulk sends are async — POST returns a `send_id`, and a background loop drives batches. Hard bounces and spam complaints clean themselves automatically via a Mailgun webhook into `/webhooks/mailgun`.

## Pick the right interface

When more than one of these is available, prefer the higher-confidence option:

| Interface | When to use | Notes |
|---|---|---|
| **MCP tools** (`minigun.*`) | Whenever the client has the MiniGun MCP server connected. | Destructive tools (`send_bulk`, `send_single`, `unsubscribe_contact`, `delete_contact`, `resume_send`) carry a `DestructiveHint`, so the client renders a confirmation prompt for the user. Use this path by default — it gives the user a chance to veto. |
| **`minigun` CLI** | When MCP isn't connected, when the user is already in a shell, when scripting in a CI step, or when the operation is part of a larger bash pipeline. | Two env vars are required (`MINIGUN_API_URL`, `MINIGUN_API_TOKEN`); install with `go install github.com/ranaroussi/minigun/cli/cmd/minigun@latest`. |
| **Raw HTTP / SDK** | Inside an application's runtime code, or when the user is building a service that *uses* MiniGun rather than operating it. | Single-file SDKs exist for PHP / Python / TypeScript / Go under `sdks/<lang>/`. Prefer these over hand-rolled HTTP. |

If you're unsure whether MCP is available, look for tools named `minigun.health`, `minigun.send_bulk`, etc. in your tool list. If they exist, use them.

## The surface, at a glance

| Operation | MCP tool | CLI | HTTP |
|---|---|---|---|
| Liveness check | `minigun.health` | `minigun health` | `GET /healthz` |
| Create company | `minigun.create_company` | `minigun company create` | `POST /companies` |
| Create list | `minigun.create_list` | `minigun list create` | `POST /lists` |
| Add / upsert contact | `minigun.add_contact` | `minigun contact add` | `POST /lists/{list}/contacts` |
| Admin-side unsubscribe | `minigun.unsubscribe_contact` | `minigun contact unsubscribe` | `POST /lists/{list}/unsubscribe` |
| **Hard-delete contact** | `minigun.delete_contact` | `minigun contact delete` | `DELETE /contacts/{idOrEmail}` |
| Send a single transactional | `minigun.send_single` | `minigun send single` | `POST /send/single` |
| Send a bulk campaign | `minigun.send_bulk` | `minigun send bulk` | `POST /send/bulk` |
| Check send status | `minigun.get_send_status` | `minigun send status` | `GET /send/{id}` |
| Get send stats | `minigun.get_send_stats` | `minigun send stats` | `GET /send/{id}/stats` |
| Resume a paused send | `minigun.resume_send` | `minigun send resume` | `POST /send/{id}/resume` |

## Pick the right send mode

There are **two** send shapes; picking the wrong one is the most common operator mistake.

### `send_single` — transactional

- One recipient, one message. Returns 202 with a `send_id`.
- **By default no `list:` is attached**, so no unsubscribe footer is injected and no List-Unsubscribe headers are added. This is correct for receipts, password resets, account notifications — the recipient is not a marketing subscriber.
- Use `list:` on a single send only when the recipient *is* a subscriber and the message is a one-off update to them specifically (e.g. "hey, you specifically asked us to email when X ships"). The presence of `list:` activates the per-recipient unsub link and List-Unsubscribe headers, which Gmail's bulk-sender rules require for any promotional content.

### `send_bulk` — campaign

- Goes to every subscribed contact on a list. Required: `list:`, `subject:`, `from:`, and one of `md:` / `html:`.
- Always carries List-Unsubscribe + RFC 8058 one-click headers and always injects a per-recipient unsub footer (auto-injected if your template doesn't include one).
- Async: the first batch runs inline before the 202 response, then the worker self-chains to drive the remaining batches. A worker crash mid-send recovers via a once-a-minute cron sweep — you don't need to babysit it.
- Knobs that matter:
  - `batch_size` (default 500) — how many recipients per Mailgun POST.
  - `throttle_ms` (default 1000) — milliseconds to sleep between batches.
  - `test_mode` (default false) — when true, sets Mailgun's `o:testmode=yes` so messages are accepted and logged but **not delivered**. Use this *every time* you're sending a new template, a new audience, or a draft you haven't proofread yourself.
  - `unsub_mode` (default `local`) — `local` (MiniGun renders the unsub page), `redirect` (MiniGun records the unsub then redirects to your thank-you URL), or `external` (you host the entire flow).

## Authoring in Markdown

Templates are Markdown. MiniGun renders both an HTML and a plain-text part. Variables are written as `{{first_name | "there"}}` — the part after `|` is the default when the contact has no value for that key. Variables that don't exist (and have no default) render as empty strings, not as literal `{{whatever}}`.

```markdown
Hi {{first_name | "there"}},

Big news this week — here's the [full update]({{post_url | "https://example.com/blog/12"}}).

If you've stopped finding these useful, you can {{unsubscribe}}.

Cheers,
Ran
```

Best practices:

- **Always assume `first_name` may be empty.** Use the `| "there"` default. A "Hi ," opener is a deliverability red flag.
- **Set `preheader:`** — this is the inbox-preview snippet next to the subject. Without it, Gmail/Apple Mail grab the first line of body text, which is almost always wrong.
- **Don't write your own unsub footer** unless you have a reason to. MiniGun auto-injects one in both HTML and text if it doesn't see `{{unsubscribe}}` anywhere in the body. Trust the auto-injection.
- **HTML wrapper template via `template:`** — for branded campaigns, pass an HTML file with `{{content}}` or `<!-- content -->` as the placeholder; the rendered Markdown gets dropped in. Keeps copy clean of style cruft.

## Operating a campaign — the standard playbook

When the user says "send the newsletter" or "fire off this campaign", **do not just call `send_bulk`**. Walk this list first, even if it slows you down by a few seconds:

1. **Verify the surface is up.** Run `minigun.health` or `GET /healthz`. If it errors, stop and tell the user — sending to a dead worker leaves no trail.
2. **Verify the list exists and has subscribers.** Either `minigun.get_list` (via list resource) or `minigun list get <slug>` — note the `subscribed_count`. If it's 0 you're about to send to nobody; if it's 100k and the user thought it was 1k, stop.
3. **Read the markdown.** Open the file. Confirm the subject line isn't a placeholder, no `{{first_name}}` without a default, no broken links, no embarrassing typos that a human would catch. Don't send copy you haven't read.
4. **Confirm the `from:` address.** It should match a verified sending domain in Mailgun. If the user says `From: Ran <ran@example.com>` but `example.com` isn't configured in Mailgun, the send will be rejected at the API. Better to catch this now than after dispatch.
5. **Test mode for new templates.** If this is the first time the user is sending this template, or the audience is new, or the user is unsure — dispatch with `test_mode: true` first. Wait for the send to complete (`get_send_status` until `status=done`). Inspect the Mailgun logs the user gets emailed. Then dispatch the real thing.
6. **Dispatch.** Call `send_bulk` with the right knobs (see "IP warming" below for `batch_size` / `throttle_ms`).
7. **Poll for completion.** Use `get_send_status` (or `minigun send status <id> --watch`) until `status=done` or `failed`. Don't move on until you know it finished.
8. **Pull stats.** After the send is done, `get_send_stats` gives you delivered / opened / clicked / bounced / complained / unsubscribed. Surface these to the user. MiniGun pulls stats on a front-loaded schedule (+0h, +1h, +6h, +24h, +48h, +5d after completion) and persists them, so re-running `get_send_stats` later returns historically frozen numbers — Mailgun itself only retains event logs for 5 days.

## When a send fails or stalls

Sends can sit in `paused` (an internal error) or be detected as `stuck` by the every-minute cron sweep. To recover:

1. **`get_send_status` first.** Look at the `error` field and the `last_batch_at` timestamp. Decide whether the failure cause is transient (Mailgun 5xx, transient DNS) or persistent (bad sending domain, malformed template).
2. **If transient: `resume_send` without `force`.** This re-queues from the last completed batch.
3. **If a batch was left `in_flight` and you need to resume anyway: `resume_send` with `force=true`.** **WARN THE USER FIRST** — Mailgun may have already accepted the in-flight batch on its side. Forcing a resume can duplicate-send those recipients. Only force when the user has explicitly accepted that risk (often it's better to leave the in-flight batch lost than to duplicate).
4. **If the cause is persistent (e.g. bad sending domain):** there's no point resuming. Help the user fix the underlying config (Mailgun dashboard, DNS records) and then start a fresh send.

## List hygiene — almost always automatic

MiniGun has a Mailgun webhook at `POST /webhooks/mailgun` that auto-deletes hard-bouncing addresses and spam-complainers in real time. **You do not need to clean these manually** — assume the webhook is running and the list is self-healing.

When you *do* need to act manually:

- **`delete_contact`** — hard purge: removes the contact + every subscription on every list + every audit row. Use for:
  - Importing a hard-bounce list from a previous email provider before doing the first send (`for email in bounces.csv: minigun.delete_contact(email)`).
  - GDPR / CCPA right-to-be-forgotten requests.
  - One-off corrections (a contact you added by mistake).
- **`unsubscribe_contact`** — admin-side opt-out: marks the row with `subscribed=0` but **preserves the row** so future re-imports don't silently re-subscribe them. Use for:
  - User-initiated opt-outs that came in through a non-email channel (phone, support ticket).
  - Adding a list of opt-outs migrated from a previous provider.

**The distinction matters operationally:**

- `delete` then re-import = re-subscribed. (Row was purged, has no memory of opting out.)
- `unsubscribe` then re-import = stays unsubscribed. (Row is preserved with `subscribed=0`.)

If a user asks you to "remove" or "delete" a contact, **ask which they mean**:
- "Are they a hard bounce (mailbox no longer exists)? → `delete_contact`."
- "Did they ask to opt out? → `unsubscribe_contact` so they stay opted out if they end up in a future import."

## Mailgun deliverability — the operator playbook

This is the part most operators don't know. Encoding it here so you can keep the user safe.

### IP warming (the first month)

When a sending IP is new (or returns to active use after >30 days dormant), inbox providers throttle it heavily. Ramp daily volume on a schedule:

| Day  | Daily cap | Notes |
|------|-----------|-------|
| 1–3  | 50        | Hand-pick your most engaged 50 subscribers. |
| 4–7  | 200       | Spread across the day, not all at once. |
| 8–14 | 1,000     | Continue to favor highest-engagement segments. |
| 15–21| 5,000     | Now you can mix in less-engaged subscribers. |
| 22–28| 20,000    | At this volume Gmail's bulk-sender rules apply: `List-Unsubscribe` + `One-Click-Unsubscribe` headers required (MiniGun does this automatically on `send_bulk`). |
| 29+  | Full list | Volume cap effectively lifted; reputation is now established. |

On a cold IP, set `batch_size` to **50–100** (not 500) and `throttle_ms` to **5000+** (not 1000) so you don't blast a fresh IP. The default 500/1000ms is right once the IP is warm.

### Authentication: SPF, DKIM, DMARC

All three must be set up on every sending domain. Mailgun's dashboard guides the user through SPF and DKIM. DMARC the user owns:

- **Start with `p=quarantine`** (failed messages go to spam, not rejected).
- **Monitor `rua=` reports for ~30 days.** Look for `dkim=pass dmarc=pass` on legitimate mail and `dmarc=fail` on spoofing attempts.
- **Graduate to `p=reject`** once stats look clean. This is the strongest stance and signals to inbox providers that you take spoofing seriously.

### Reputation monitoring

- **Register every sending domain at https://postmaster.google.com.** Gmail Postmaster Tools shows you spam rate, IP reputation, domain reputation, authentication pass rate. If any of these dip into "Bad" territory, stop sending and triage immediately.
- **Spam rate must stay under 0.10% per Gmail's bulk-sender rules.** The auto-cleanup webhook helps here — every complaint is one fewer subscriber who can complain again.
- **Bounce rate above 5% is a red flag.** This usually means a stale list. Either the auto-cleanup hasn't caught up (give it time) or the user just imported a low-quality list (advise them to clean it first).

### Subject lines & content

These get sends sent to spam regardless of reputation:

- All-caps subject lines.
- Excessive punctuation (`!!!`, `???`).
- "FREE", "$$$", "URGENT", "ACT NOW", etc. in the subject.
- High image-to-text ratio in HTML body (>40%).
- Single huge `<a>` tag covering most of the email.
- Bare URLs that don't match the visible link text.

If you see any of these in the user's draft, flag them before dispatch.

## Common recipes

### "Draft and send a newsletter to list X"

1. `minigun.get_list(slug='X')` — confirm it exists and has subscribers.
2. Read the draft markdown if it's on disk; if not, ask the user where the copy is or draft from a brief.
3. If drafting yourself: write tight, punchy copy. Aim for one CTA per email. Set `preheader:` to a one-sentence summary of what's in the body.
4. `send_bulk` with `test_mode=true` first. Wait for `status=done`.
5. Pull the testmode logs from Mailgun dashboard (Mailgun emails them to the configured account). Confirm rendering looks right.
6. `send_bulk` with `test_mode=false` for the real run.
7. Poll `get_send_status` until done.
8. Surface `get_send_stats` numbers to the user when done.

### "Send a transactional welcome to a new signup"

1. Decide whether the recipient is on a list. If yes (and you want their unsub link to work), pass `list:`. If no (purely transactional, e.g. password reset), omit `list:`.
2. `send_single` with `to:`, `from:`, `subject:`, `company:`, and `md:`. Set `preheader:`.
3. 202 returns immediately. For most transactional flows you don't need to poll — Mailgun's own retry logic handles delivery.

### "How did yesterday's send do?"

1. List recent sends: `GET /sends?limit=20` or `minigun send list`. Find the one from yesterday.
2. `get_send_stats` on the send_id. Surface delivered / opened / clicked / bounced / complained / unsubscribed.
3. If asked for comparative analysis: compare against the historical average for that list (the user can pull a few previous sends and you can average them inline).

### "Migrate a bounce list from MailerLite / Brevo / Mailchimp"

1. Get the bounce CSV from the user.
2. For each row, call `delete_contact(email)`. Idempotent — already-purged addresses return `already-gone` and continue.
3. Once done, the next bulk send to this list won't include those addresses, so the next send's bounce rate should drop into the green.
4. If the user *also* has a separate opt-out list (people who unsubscribed but whose mailboxes are still alive), import those with `unsubscribe_contact` instead. This preserves the opt-out record across future imports.

### "Resume a stuck send"

1. `get_send_status` on the send_id. Note the `status`, `error`, and `last_batch_at`.
2. If `status=paused` or `status=failed` with no `in_flight` batches: `resume_send` without `force`.
3. If a batch is `in_flight`: warn the user about duplicate-send risk, get explicit confirmation, then `resume_send(force=true)`.

### "I'm a new sender — what's my first week look like?"

Walk them through the IP warming table above. Suggest sending the first batch (50 messages) by hand-picking their 50 most engaged subscribers (recent opens, recent clicks). Have them register Postmaster Tools before day 1, not after. Confirm SPF/DKIM/DMARC are set up before any send.

## Anti-patterns (red flags — push back on the user)

| If the user asks for... | Don't. Instead... |
|---|---|
| "Just send the campaign, skip the test mode" | If it's a fresh template or fresh audience, push back. Test mode costs 30 seconds and catches embarrassing errors. |
| "Send this promo via `send_single`, no `list:`" | A promotional / marketing message without a List-Unsubscribe header violates Gmail's bulk-sender rules and gets you flagged. Push back and propose `send_bulk` to a list-of-one if needed. |
| "Manually delete the hard bounces from my list" | The Mailgun webhook is doing this in real time. Manual cleanup is redundant; check `complaint_events` and Mailgun's bounce logs to verify the webhook is firing. |
| "Resume the stuck send with `--force`, it's been an hour" | If a batch is `in_flight`, `--force` can duplicate-send. Walk the user through the trade-off explicitly before doing it. |
| "Crank `batch_size` to 5000 — we're behind schedule" | On anything but a fully warmed IP this gets the IP throttled by Gmail. Stay at 500 (or lower on cold IPs). Throttle the human, not the inbox provider. |
| "Use `delete_contact` for someone who unsubscribed" | `unsubscribe_contact` is correct here. Delete purges the row; if the contact ends up in a future CSV import, they'll get re-subscribed and you'll be back to square one. |
| "Set DMARC to `p=reject` on day 1" | Start at `p=quarantine`, monitor `rua=` reports for ~30 days, then graduate. Going straight to reject on a new domain can bounce legitimate mail you didn't know about (forwarders, mailing lists). |

## When you're stuck

- Re-read the [top-level README](https://github.com/ranaroussi/minigun/blob/main/README.md) — every feature has a dedicated section.
- The [API reference](https://github.com/ranaroussi/minigun/blob/main/README.md#api) lists every endpoint with method and path.
- The [CLI walkthrough](https://github.com/ranaroussi/minigun/blob/main/docs/cli.md) has examples for every command.
- For deliverability questions outside this skill's playbook, point the user at [Gmail's bulk-sender guidance](https://support.google.com/mail/answer/81126) and [Mailgun's deliverability hub](https://www.mailgun.com/blog/deliverability/).

## What this skill is *not* for

- **Developing MiniGun itself** (the server code, the worker, the CLI internals). This skill teaches operating MiniGun; modifying its source is a regular code-editing task.
- **Bulk emailing that doesn't go through Mailgun.** If the user is on AWS SES, Postmark, SendGrid, etc., this skill doesn't apply — though the deliverability playbook (warming, DMARC, Postmaster) translates directly.
- **Email *receiving* / inbound parsing.** MiniGun is send-only.
