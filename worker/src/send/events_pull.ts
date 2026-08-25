import { Env, engagementStatsEnabled } from '../env';
import { fetchEvents, MailgunAPIError } from '../lib/mailgun';
import {
  DueEventPullRow,
  checkpointEventPullThrough,
  clickStmt,
  engagementStmt,
  getSendListID,
  listDueEventPulls,
  lookupContactIDsByEmails,
  messageEngagementStmt,
  normalizeEvent,
  recordEventPullError,
  recordEventPullProgress,
} from '../store/events';
import { hasActiveSend } from '../store/sends';

// ---------------------------------------------------------------------------
// Schedule
// ---------------------------------------------------------------------------

// Burst beats: relative to send.created_at, fire at these offsets.
const BURST_OFFSETS_MS = [
  0,
  1 * 60 * 60 * 1000,        // +1h
  6 * 60 * 60 * 1000,        // +6h
  24 * 60 * 60 * 1000,       // +24h
];

// After the burst is exhausted, pull every DAILY_INTERVAL_MS until the
// archive window closes.
const DAILY_INTERVAL_MS = 24 * 60 * 60 * 1000;

// Total archive window, anchored to send.created_at. After this elapses
// the send is frozen — no more polls, no more reads against Mailgun.
// 14 days: opens/clicks are overwhelmingly front-loaded (first few days),
// so the second half of Mailgun's 30-day retention folds almost nothing
// while keeping the send in the daily-pull candidate set and writing a
// checkpoint each beat. Freezing at 14d trims that write/read tail with
// negligible engagement loss. Raise back toward 30d if long-tail opens
// ever matter more than the D1 budget.
export const ARCHIVE_MAX_AGE_MS = 14 * 24 * 60 * 60 * 1000;

// Hard cap on pages fetched per send per beat. Each page is one Mailgun
// fetch + one batched contact lookup + chunked D1 write batches, so this
// bounds the CPU/subrequest budget a single cron tick spends on one send
// (300 events/page × 16 pages = 4.8k events/beat). A send with a larger
// backlog is drained across consecutive cron ticks: while capped, the
// beat checkpoints its watermark WITHOUT bumping events_pulls_count, so
// nextDueAt keeps it immediately due until it catches up. The per-page
// checkpoint means a CPU-killed beat still keeps the pages it finished —
// only the dense same-second delivery burst right after a send risks
// hitting the limit at this cap, and that section is idempotent.
const MAX_PAGES_PER_BEAT = 16;

// Max statements per D1 batch() call. A page of 300 events can emit up to
// ~3 writes each; we flush in chunks so no single batch is oversized.
const BATCH_CHUNK = 90;

// Returns the timestamp of the next scheduled pull beat for `send`, or
// null if the send is past the archive window (caller should freeze).
//
// Burst phase (pulls 0..3): use the BURST_OFFSETS_MS lookup.
// Daily phase (pulls 4+):   last_pulled_at + 24h, capped by the window.
//
// Anchoring everything to created_at (not completed_at) means the cron
// can dispatch without knowing the send's lifecycle status. For typical
// sends (complete in minutes), +1h-after-created ≈ +1h-after-completed.
// For long-running sends (slow re-warms over weeks), pulling during
// the send gives operators real-time deliverability monitoring.
export function nextDueAt(row: {
  created_at_ms: number;
  events_pulls_count: number;
  events_last_pulled_at_ms: number | null;
}): number | null {
  if (row.events_pulls_count < BURST_OFFSETS_MS.length) {
    return row.created_at_ms + BURST_OFFSETS_MS[row.events_pulls_count]!;
  }
  if (row.events_last_pulled_at_ms === null) {
    // Shouldn't happen — pulls_count >= 4 implies we've pulled — but be
    // defensive and treat this as "due now."
    return Date.now();
  }
  const next = row.events_last_pulled_at_ms + DAILY_INTERVAL_MS;
  if (next > row.created_at_ms + ARCHIVE_MAX_AGE_MS) return null;
  return next;
}

// ---------------------------------------------------------------------------
// Orchestration
// ---------------------------------------------------------------------------

// Cron entrypoint. Called from the worker's scheduled handler. No-ops
// when ENGAGEMENT_STATS_ENABLED is unset (engagement retrieval feature flag).
export async function pullDueSendEvents(env: Env, limit = 20): Promise<void> {
  if (!engagementStatsEnabled(env)) return;
  // Pause retrieval while a send is in flight. The events-pull can burn the
  // whole scheduled-handler CPU budget folding the dense delivery burst of a
  // large send, which starves sweepStuckSends and wedges the very send that
  // is generating those events. Deferring the pull by a tick (or a few, for
  // long sends) is harmless — the burst/daily schedule is anchored to
  // created_at and stays "due" until it runs, so nothing is dropped.
  try {
    if (await hasActiveSend(env.DB)) return;
  } catch (err) {
    console.error('events-pull: active-send check', err);
    return;
  }
  const now = Date.now();
  let candidates: DueEventPullRow[];
  try {
    candidates = await listDueEventPulls(env.DB, now, ARCHIVE_MAX_AGE_MS, limit * 2);
  } catch (err) {
    console.error('events-pull: list candidates', err);
    return;
  }
  // Filter through nextDueAt (the SQL pulled "non-frozen, within window"
  // but the burst-vs-daily phase logic is in JS so the index can stay
  // simple). Take up to `limit` candidates whose due time has arrived.
  const picked: DueEventPullRow[] = [];
  for (const c of candidates) {
    if (picked.length >= limit) break;
    const due = nextDueAt(c);
    if (due === null) {
      // Send is past the archive window but still wasn't marked complete
      // (could happen if it never had completed_at set). Run one final
      // pull to flush remaining events, then it'll freeze.
      picked.push(c);
      continue;
    }
    if (due <= now) picked.push(c);
  }
  for (const send of picked) {
    try {
      await pullEventsForOneSend(env, send);
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      console.error('events-pull: send', send.send_id, msg);
      try {
        await recordEventPullError(env.DB, send.send_id, msg);
      } catch (innerErr) {
        console.error('events-pull: record error', send.send_id, innerErr);
      }
    }
  }
}

// Pull events for one send. Pages through Mailgun's events API until
// either the cursor exhausts or MAX_PAGES_PER_SEND is hit. Each event is
// normalized, its recipient resolved to a contact_id, and folded into
// the two engagement rollups (contact_message_engagement always;
// contact_engagement when the send is list-tied).
//
// The window begins strictly AFTER the previous pull's highest event
// timestamp (default: the send's created_at on the first pull). Because
// Mailgun returns events in time order and we advance the watermark to
// the last event we saw, the next pull never re-fetches an
// already-counted event — so the per-call counter increments stay
// correct without any dedup ledger.
export async function pullEventsForOneSend(
  env: Env,
  send: DueEventPullRow,
): Promise<{ inserted: number; pages: number; frozen: boolean }> {
  const nowMs = Date.now();
  // +1ms makes the lower bound exclusive at storage (ms) granularity.
  //
  // Known tradeoffs of the no-ledger design (accepted, not bugs):
  //  1. Counter drift on crash/retry. Events are folded into the rollups
  //     before the watermark is persisted below, and the counter UPSERTs
  //     (total_opens+1, messages_since_last_engagement+1, ...) are not
  //     idempotent. If this run dies mid-pull or recordEventPullProgress
  //     fails, the next beat re-applies the events already folded,
  //     inflating the *counts*. Timestamp fields converge regardless
  //     (MIN/MAX), so only integer counters drift; recency-based hygiene
  //     is unaffected.
  //  2. Same-millisecond skip at the exclusive boundary. The next pull
  //     resumes at lastEventTs+1ms, so any event sharing that exact ms
  //     not seen this run — a late, eventually-consistent arrival, or one
  //     stranded past the MAX_PAGES_PER_SEND cap — is skipped permanently.
  //     Bounded-probability given ms granularity and the 15k-events/send
  //     cap; the cost of holding no cursor.
  const beginMs =
    send.events_last_pulled_through_ms !== null
      ? send.events_last_pulled_through_ms + 1
      : send.created_at_ms;
  const endMs = nowMs;

  // One list_id lookup per send (cached locally). Singles without a
  // list_id return null and skip the contact_engagement UPSERT.
  const listID = await getSendListID(env.DB, send.send_id);

  let inserted = 0;
  let pages = 0;
  let pageURL: string | undefined;
  // Highest event timestamp durably checkpointed so far. The next pull
  // resumes strictly after it. Advanced per-page (not just at the end) so
  // a CPU-killed beat still makes forward progress.
  let lastEventTs = lastThroughOnEntry(send);
  let cappedWithMorePages = false;

  while (pages < MAX_PAGES_PER_BEAT) {
    const page = pageURL
      ? await fetchEvents(env, { pageURL })
      : await fetchEvents(env, {
          domain: send.from_domain,
          tag: send.send_id,
          beginMs,
          endMs,
          limit: 300,
        });
    pages++;

    if (!page.items || page.items.length === 0) break;

    // Normalize the page, then resolve every recipient in ONE read
    // instead of a serial lookup per event (the dominant CPU cost on
    // large sends).
    const events = page.items
      .map(normalizeEvent)
      .filter((e): e is NonNullable<typeof e> => e !== null);
    const contactByEmail = await lookupContactIDsByEmails(
      env.DB,
      events.map((e) => e.recipient),
    );

    // Build all writes for this page, then flush them in ordered batches.
    // Order is preserved across chunks so the non-idempotent counter
    // UPSERTs (total_opens + 1, ...) accumulate correctly.
    const stmts: D1PreparedStatement[] = [];
    let pageMaxTs = 0;
    for (const ev of events) {
      if (ev.event_timestamp_ms > pageMaxTs) pageMaxTs = ev.event_timestamp_ms;
      const contactID = contactByEmail.get(ev.recipient);
      if (!contactID) continue; // event for an unknown address

      inserted++;
      if (isMessageEvent(ev.event)) {
        const s = messageEngagementStmt(
          env.DB,
          send.send_id,
          contactID,
          listID,
          ev.event,
          Math.floor(ev.event_timestamp_ms / 1000),
          ev.severity,
          ev.reason,
        );
        if (s) stmts.push(s);
      }
      if (listID !== null && isEngagementEvent(ev.event)) {
        const s = engagementStmt(env.DB, contactID, listID, ev.event, ev.event_timestamp_ms);
        if (s) stmts.push(s);
      }
      if (ev.event === 'clicked' && ev.url) {
        const s = clickStmt(
          env.DB,
          send.send_id,
          contactID,
          listID,
          ev.url,
          Math.floor(ev.event_timestamp_ms / 1000),
        );
        if (s) stmts.push(s);
      }
    }

    for (let i = 0; i < stmts.length; i += BATCH_CHUNK) {
      await env.DB.batch(stmts.slice(i, i + BATCH_CHUNK));
    }

    // Durable per-page checkpoint: advance the watermark to this page's
    // max event timestamp WITHOUT bumping events_pulls_count, so a beat
    // that dies on the next page resumes after this one.
    if (pageMaxTs > lastEventTs) {
      lastEventTs = pageMaxTs;
      await checkpointEventPullThrough(env.DB, send.send_id, {
        last_pulled_through_ms: lastEventTs,
        inserted,
      });
      inserted = 0; // already folded into events_archive_count above
    }

    // Follow Mailgun's pagination cursor. When items is empty OR the
    // next URL is missing/identical, we've reached the end.
    const next = page.paging?.next;
    if (!next || next === pageURL) break;
    pageURL = next;
    if (pages >= MAX_PAGES_PER_BEAT) {
      // Exiting with an outstanding next page — this scheduled pull
      // hasn't caught up. Don't bump the burst counter; the next cron
      // tick continues paging from the checkpointed watermark.
      cappedWithMorePages = true;
    }
  }

  // While still draining a backlog (capped with more pages), the per-page
  // checkpoint already persisted forward progress and we deliberately do
  // NOT advance the burst schedule. Only when this scheduled pull has
  // caught up do we bump events_pulls_count and (if past the window)
  // freeze the send.
  if (!cappedWithMorePages) {
    const newThroughMs = lastEventTs > 0 ? lastEventTs : beginMs - 1;
    const frozen = nowMs >= send.created_at_ms + ARCHIVE_MAX_AGE_MS;
    await recordEventPullProgress(env.DB, send.send_id, {
      last_pulled_at_ms: nowMs,
      last_pulled_through_ms: newThroughMs,
      inserted,
      freeze: frozen,
    });
    return { inserted, pages, frozen };
  }

  return { inserted, pages, frozen: false };
}

// The watermark already covered on entry — events strictly after it are
// what this beat fetches. Used to seed lastEventTs so an empty/no-new
// page doesn't rewind the checkpoint.
function lastThroughOnEntry(send: DueEventPullRow): number {
  return send.events_last_pulled_through_ms !== null
    ? send.events_last_pulled_through_ms
    : 0;
}

// isEngagementEvent reports whether the event type drives an update to
// the per-(contact, list) contact_engagement summary.
function isEngagementEvent(eventType: string): boolean {
  return eventType === 'delivered' || eventType === 'opened' || eventType === 'clicked';
}

// isMessageEvent reports whether the event type updates the per-(send,
// contact) message engagement row. Broader than isEngagementEvent (the
// per-list rollup gate): cme also records acceptance, failure, complaint,
// and unsubscribe so a single message's full lifecycle is queryable.
function isMessageEvent(eventType: string): boolean {
  switch (eventType) {
    case 'accepted':
    case 'delivered':
    case 'opened':
    case 'clicked':
    case 'failed':
    case 'rejected':
    case 'complained':
    case 'unsubscribed':
      return true;
    default:
      return false;
  }
}

// Surface for direct on-demand invocation (POST /mailgun/events/pull?send_id=).
// Phase 3 will wire this to an HTTP route; exposing it here for now keeps
// the dependency direction clean.
export { MailgunAPIError };
