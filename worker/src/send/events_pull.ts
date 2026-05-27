import { Env, eventsArchiveEnabled } from '../env';
import { fetchEvents, MailgunAPIError } from '../lib/mailgun';
import {
  DueEventPullRow,
  applyEventToEngagement,
  getSendListID,
  insertEventIfNew,
  listDueEventPulls,
  normalizeEvent,
  recordEventPullError,
  recordEventPullProgress,
} from '../store/events';

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
// 30 days matches Mailgun's paid-tier retention; beyond it there's
// nothing pullable that we haven't already seen.
export const ARCHIVE_MAX_AGE_MS = 30 * 24 * 60 * 60 * 1000;

// Overlap window: every pull re-fetches the last OVERLAP_MS of events,
// relying on the UNIQUE(mailgun_event_id) constraint to dedupe. 6h is
// generous against Mailgun's "events arrive out of order, sometimes
// delayed by hours" behavior; bounded enough to keep each pull's cost low.
const OVERLAP_MS = 6 * 60 * 60 * 1000;

// Hard cap on pages fetched per send per beat. Prevents one cron tick
// from saturating the worker after a long Mailgun outage (300 events/page
// × 50 pages = 15k events max per send per beat).
const MAX_PAGES_PER_SEND = 50;

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
// when EVENTS_ARCHIVE_ENABLED is unset (Phase 2+ feature flag).
export async function pullDueSendEvents(env: Env, limit = 20): Promise<void> {
  if (!eventsArchiveEnabled(env)) return;
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
// either the cursor exhausts or MAX_PAGES_PER_SEND is hit. Each event
// is normalized, INSERT OR IGNORE'd into mailgun_events, and (if newly
// inserted AND the send is list-tied AND the contact is resolved)
// applied to contact_engagement.
//
// Persists the per-send watermark on success regardless of how many
// events were actually new — the goal of advancing the watermark is
// "we've covered this window of time," not "we wrote new rows."
export async function pullEventsForOneSend(
  env: Env,
  send: DueEventPullRow,
): Promise<{ inserted: number; pages: number; frozen: boolean }> {
  const nowMs = Date.now();
  const beginMs =
    (send.events_last_pulled_through_ms ?? send.created_at_ms) - OVERLAP_MS;
  const endMs = nowMs;

  // One list_id lookup per send (cached locally). Singles without a
  // list_id return null and skip the engagement-summary UPSERT.
  const listID = await getSendListID(env.DB, send.send_id);

  let inserted = 0;
  let pages = 0;
  let pageURL: string | undefined;

  while (pages < MAX_PAGES_PER_SEND) {
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

    for (const raw of page.items) {
      const ev = normalizeEvent(raw, send.from_domain, send.send_id);
      if (!ev) continue;
      let res: { inserted: boolean; contact_id: string | null };
      try {
        res = await insertEventIfNew(env.DB, ev);
      } catch (err) {
        // A single malformed row shouldn't kill the whole pull. Log and
        // continue — we'll try again on the next beat (and the UNIQUE
        // constraint deduplicates correctly so re-trying is safe).
        console.error('events-pull: insert', send.send_id, ev.mailgun_event_id, err);
        continue;
      }
      if (!res.inserted) continue;
      inserted++;
      if (listID && res.contact_id) {
        try {
          await applyEventToEngagement(
            env.DB,
            res.contact_id,
            listID,
            ev.event,
            ev.event_timestamp_ms,
          );
        } catch (err) {
          // Engagement-summary failure is non-fatal — the raw archive
          // is the source of truth, and we can rebuild contact_engagement
          // from mailgun_events offline if it ever drifts.
          console.error(
            'events-pull: engagement upsert',
            send.send_id,
            res.contact_id,
            err,
          );
        }
      }
    }

    // Follow Mailgun's pagination cursor. When items is empty OR the
    // next URL is missing/identical, we've reached the end.
    const next = page.paging?.next;
    if (!next || next === pageURL) break;
    pageURL = next;
  }

  const frozen = nowMs >= send.created_at_ms + ARCHIVE_MAX_AGE_MS;

  try {
    await recordEventPullProgress(env.DB, send.send_id, {
      last_pulled_at_ms: nowMs,
      last_pulled_through_ms: endMs - OVERLAP_MS,
      inserted,
      freeze: frozen,
    });
  } catch (err) {
    // Re-raise so the orchestrator records the error against the send.
    throw err;
  }

  return { inserted, pages, frozen };
}

// Surface for direct on-demand invocation (POST /mailgun/events/pull?send_id=).
// Phase 3 will wire this to an HTTP route; exposing it here for now keeps
// the dependency direction clean.
export { MailgunAPIError };
