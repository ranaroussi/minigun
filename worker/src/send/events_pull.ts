import { Env, eventsArchiveEnabled } from '../env';
import { fetchEvents, MailgunAPIError } from '../lib/mailgun';
import {
  DueEventPullRow,
  applyEventToEngagement,
  getSendListID,
  insertEventIfNew,
  listDueEventPulls,
  listUnappliedEngagementEvents,
  markEngagementApplied,
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
  // Phase 5: replay any raw events whose engagement update was skipped
  // in a previous tick (insert succeeded but applyEventToEngagement
  // failed). Bounded so a backlog drains over multiple ticks.
  await replayUnappliedEngagement(env, 500);
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
  // Phase 5: highest event timestamp processed in this batch. Used for
  // conservative watermark advance when MAX_PAGES_PER_SEND caps the
  // loop with more pages remaining (the H2 fix). The 6h overlap on the
  // next pull re-fetches anything we missed, and UNIQUE deduplicates.
  let lastEventTs = 0;
  let cappedWithMorePages = false;

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
      if (ev.event_timestamp_ms > lastEventTs) lastEventTs = ev.event_timestamp_ms;
      let res: { inserted: boolean; id: string; contact_id: string | null };
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
      const needsEngagement =
        listID !== null && res.contact_id !== null && isEngagementEvent(ev.event);
      if (needsEngagement) {
        try {
          await applyEventToEngagement(
            env.DB,
            res.contact_id!,
            listID!,
            ev.event,
            ev.event_timestamp_ms,
          );
        } catch (err) {
          // Engagement-summary failure is non-fatal here — the row is
          // archived and engagement_applied stays 0, so the replay scan
          // (replayUnappliedEngagement) will reconcile it next tick.
          console.error(
            'events-pull: engagement upsert',
            send.send_id,
            res.contact_id,
            err,
          );
          continue;
        }
      }
      try {
        await markEngagementApplied(env.DB, res.id);
      } catch (err) {
        console.error('events-pull: mark engagement applied', send.send_id, res.id, err);
      }
    }

    // Follow Mailgun's pagination cursor. When items is empty OR the
    // next URL is missing/identical, we've reached the end.
    const next = page.paging?.next;
    if (!next || next === pageURL) break;
    pageURL = next;
    if (pages >= MAX_PAGES_PER_SEND) {
      // About to exit the loop with an outstanding next-page URL —
      // flag so the watermark advance below stays conservative.
      cappedWithMorePages = true;
    }
  }

  // Watermark advance strategy (Phase 5):
  //   * Normal exhaustion → advance to (endMs - OVERLAP_MS); the 6h
  //     overlap on the next pull catches late arrivals.
  //   * Page-cap with more pages → advance only to lastEventTs (highest
  //     event processed). The next pull picks up from lastEventTs -
  //     OVERLAP_MS and re-fetches the remaining pages via a new
  //     begin/end window — UNIQUE deduplicates already-archived events.
  //   * Page-cap but no events processed (unusual) → keep the existing
  //     watermark to avoid going backwards.
  let newThroughMs = endMs - OVERLAP_MS;
  if (cappedWithMorePages) {
    if (lastEventTs > 0) {
      newThroughMs = lastEventTs;
    } else {
      newThroughMs = send.events_last_pulled_through_ms ?? send.created_at_ms;
    }
  }

  const frozen =
    !cappedWithMorePages && nowMs >= send.created_at_ms + ARCHIVE_MAX_AGE_MS;

  try {
    await recordEventPullProgress(env.DB, send.send_id, {
      last_pulled_at_ms: nowMs,
      last_pulled_through_ms: newThroughMs,
      inserted,
      freeze: frozen,
    });
  } catch (err) {
    // Re-raise so the orchestrator records the error against the send.
    throw err;
  }

  return { inserted, pages, frozen };
}

// isEngagementEvent reports whether the event type drives an update to
// the contact_engagement summary. Used to decide whether to mark
// engagement_applied=1 directly (non-engagement event) vs. only after
// the apply step succeeds (engagement event).
function isEngagementEvent(eventType: string): boolean {
  return eventType === 'delivered' || eventType === 'opened' || eventType === 'clicked';
}

// replayUnappliedEngagement scans for raw events whose engagement update
// never landed (insert succeeded but apply failed in a prior tick). For
// each it looks up the list_id and re-runs applyEventToEngagement, then
// flips engagement_applied=1. Bounded per tick so a backlog drains
// gracefully without saturating the cron.
//
// Idempotency: ApplyEventToEngagement's UPSERT semantics with MAX()
// guards make double-application safe for last_*_at_ms columns, but
// total_* counters would double-count if naively replayed. The flag is
// what prevents that — we only replay events explicitly marked 0, and
// we mark them 1 immediately after the apply call returns.
export async function replayUnappliedEngagement(
  env: Env,
  limit = 500,
): Promise<void> {
  let events;
  try {
    events = await listUnappliedEngagementEvents(env.DB, limit);
  } catch (err) {
    console.error('events-pull: replay list', err);
    return;
  }
  if (events.length === 0) return;
  for (const ev of events) {
    if (ev.contact_id === null || !isEngagementEvent(ev.event)) {
      try {
        await markEngagementApplied(env.DB, ev.id);
      } catch (err) {
        console.error('events-pull: replay mark', ev.id, err);
      }
      continue;
    }
    let listID: string | null;
    try {
      listID = await getSendListID(env.DB, ev.mg_send_id);
    } catch (err) {
      console.error('events-pull: replay list_id lookup', ev.id, err);
      continue;
    }
    if (!listID) {
      try {
        await markEngagementApplied(env.DB, ev.id);
      } catch (err) {
        console.error('events-pull: replay mark', ev.id, err);
      }
      continue;
    }
    try {
      await applyEventToEngagement(
        env.DB,
        ev.contact_id,
        listID,
        ev.event,
        ev.event_timestamp_ms,
      );
    } catch (err) {
      console.error('events-pull: replay apply', ev.id, err);
      continue;
    }
    try {
      await markEngagementApplied(env.DB, ev.id);
    } catch (err) {
      console.error('events-pull: replay mark', ev.id, err);
    }
  }
}

// Surface for direct on-demand invocation (POST /mailgun/events/pull?send_id=).
// Phase 3 will wire this to an HTTP route; exposing it here for now keeps
// the dependency direction clean.
export { MailgunAPIError };
