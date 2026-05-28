import { Env, eventsArchiveEnabled } from '../env';
import { fetchEvents, MailgunAPIError } from '../lib/mailgun';
import {
  DueEventPullRow,
  applyEventToEngagement,
  applyEventToMessageEngagement,
  getSendListID,
  listDueEventPulls,
  lookupContactIDByEmail,
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
  // Highest event timestamp processed in this batch — the next pull
  // resumes strictly after it. When MAX_PAGES_PER_SEND caps the loop with
  // pages remaining we still advance to lastEventTs (forward progress).
  let lastEventTs = 0;
  let cappedWithMorePages = false;
  // Memoize email→contact_id within this send so a recipient with many
  // events triggers one lookup, not one per event.
  const contactCache = new Map<string, string | null>();

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
      const ev = normalizeEvent(raw);
      if (!ev) continue;
      if (ev.event_timestamp_ms > lastEventTs) lastEventTs = ev.event_timestamp_ms;

      let contactID = contactCache.get(ev.recipient);
      if (contactID === undefined) {
        contactID = await lookupContactIDByEmail(env.DB, ev.recipient);
        contactCache.set(ev.recipient, contactID);
      }
      if (contactID === null) continue; // event for an unknown address

      inserted++;
      // Fold into both engagement tiers. cme (per-send, per-contact
      // detail) covers more event types than the per-list rollup.
      if (isMessageEvent(ev.event)) {
        await applyEventToMessageEngagement(
          env.DB,
          send.send_id,
          contactID,
          listID,
          ev.event,
          Math.floor(ev.event_timestamp_ms / 1000),
          ev.severity,
          ev.reason,
        );
      }
      if (listID !== null && isEngagementEvent(ev.event)) {
        await applyEventToEngagement(
          env.DB,
          contactID,
          listID,
          ev.event,
          ev.event_timestamp_ms,
        );
      }
    }

    // Follow Mailgun's pagination cursor. When items is empty OR the
    // next URL is missing/identical, we've reached the end.
    const next = page.paging?.next;
    if (!next || next === pageURL) break;
    pageURL = next;
    if (pages >= MAX_PAGES_PER_SEND) {
      // Exiting with an outstanding next page — don't freeze even if
      // we're past the window; the next beat continues paging.
      cappedWithMorePages = true;
    }
  }

  // Watermark advance: to lastEventTs when we processed anything,
  // otherwise hold at beginMs-1 (the through-point already covered) so an
  // empty window doesn't skip the lower bound forward past unseen events.
  let newThroughMs = beginMs - 1;
  if (lastEventTs > 0) {
    newThroughMs = lastEventTs;
  }

  const frozen =
    !cappedWithMorePages && nowMs >= send.created_at_ms + ARCHIVE_MAX_AGE_MS;

  await recordEventPullProgress(env.DB, send.send_id, {
    last_pulled_at_ms: nowMs,
    last_pulled_through_ms: newThroughMs,
    inserted,
    freeze: frozen,
  });

  return { inserted, pages, frozen };
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
