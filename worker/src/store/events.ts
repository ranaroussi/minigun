import { newMailgunEvent } from '../lib/ids';
import { MailgunEventRaw } from '../lib/mailgun';
import { nowISO } from './types';

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

// A row queued for the events-pull cron. We carry the from_domain and
// created_at_ms so the pull loop can construct Mailgun's events URL without
// re-fetching the send.
export type DueEventPullRow = {
  send_id: string;
  from_domain: string;
  created_at_ms: number;
  events_pulls_count: number;
  events_last_pulled_at_ms: number | null;
  events_last_pulled_through_ms: number | null;
};

// Subset of mailgun_events used for query endpoints in Phase 3. Kept narrow
// so the wire shape is stable as we add columns to the table.
export type MailgunEvent = {
  id: string;
  domain: string;
  mailgun_event_id: string;
  event: string;
  severity: string | null;
  recipient: string;
  recipient_domain: string;
  event_timestamp_ms: number;
  event_timestamp_iso: string;
  message_id: string | null;
  mg_send_id: string;
  contact_id: string | null;
  url: string | null;
  reason: string | null;
  tags: string | null;
  client_info: string | null;
  geolocation: string | null;
  user_variables: string | null;
  raw_payload: string;
  created_at: string;
};

// ---------------------------------------------------------------------------
// Cron-helper queries
// ---------------------------------------------------------------------------

// Selects sends whose archive window is open and whose next scheduled pull
// beat is due. Joined with sends to expose from_domain + created_at_ms in
// one round-trip. We use COALESCE(events_last_pulled_at_ms, 0) so the
// "never pulled yet" case sorts first under ASC ordering.
export async function listDueEventPulls(
  db: D1Database,
  nowMs: number,
  maxAgeMs: number,
  limit: number,
): Promise<DueEventPullRow[]> {
  // We don't try to express the burst-vs-daily schedule in SQL — that's
  // computed in nextDueAt() in the worker. The SQL just narrows to
  // "non-frozen, non-test, within the archive window" candidates and the
  // worker filters them through nextDueAt(). The partial index
  // idx_sends_event_pull_due covers (events_last_pulled_at_ms) under the
  // matching WHERE clause so candidate selection is index-direct.
  const { results } = await db
    .prepare(
      `SELECT
         id                            AS send_id,
         sending_domain                AS from_domain,
         CAST(strftime('%s', created_at) AS INTEGER) * 1000 AS created_at_ms,
         events_pulls_count,
         events_last_pulled_at_ms,
         events_last_pulled_through_ms
       FROM sends
       WHERE events_archive_complete = 0
         AND test_mode = 0
         AND status IN ('completed', 'failed', 'cancelled', 'running')
         AND sending_domain != ''
         AND CAST(strftime('%s', created_at) AS INTEGER) * 1000 > ?
       ORDER BY COALESCE(events_last_pulled_at_ms, 0) ASC
       LIMIT ?`,
    )
    .bind(nowMs - maxAgeMs, limit > 0 ? limit : 25)
    .all<DueEventPullRow>();
  return results ?? [];
}

// Records the result of a successful pull for one send. Updates the
// per-send watermark + counters, and freezes the send when it's reached
// the end of the archive window.
export async function recordEventPullProgress(
  db: D1Database,
  sendID: string,
  args: {
    last_pulled_at_ms: number;
    last_pulled_through_ms: number;
    inserted: number;
    freeze: boolean;
  },
): Promise<void> {
  await db
    .prepare(
      `UPDATE sends SET
         events_last_pulled_at_ms      = ?,
         events_last_pulled_through_ms = ?,
         events_pulls_count            = events_pulls_count + 1,
         events_archive_count          = events_archive_count + ?,
         events_archive_complete       = ?,
         events_last_pull_error        = NULL,
         updated_at                    = ?
       WHERE id = ?`,
    )
    .bind(
      args.last_pulled_at_ms,
      args.last_pulled_through_ms,
      args.inserted,
      args.freeze ? 1 : 0,
      nowISO(),
      sendID,
    )
    .run();
}

// Records an error from a Mailgun pull. We don't bump events_pulls_count
// on error — the next beat will re-attempt at the same nextDueAt(). We
// also don't advance the watermark, so the retry covers the same window.
export async function recordEventPullError(
  db: D1Database,
  sendID: string,
  errMsg: string,
): Promise<void> {
  await db
    .prepare(
      `UPDATE sends SET
         events_last_pull_error = ?,
         updated_at             = ?
       WHERE id = ?`,
    )
    .bind(errMsg, nowISO(), sendID)
    .run();
}

// ---------------------------------------------------------------------------
// Event ingestion
// ---------------------------------------------------------------------------

// Normalized payload ready to insert into mailgun_events. The shape is
// independent of Mailgun's raw event format so the persistence layer
// doesn't see the wire-format variability.
type NormalizedEvent = {
  domain: string;
  mailgun_event_id: string;
  event: string;
  severity: string | null;
  recipient: string;
  recipient_domain: string;
  event_timestamp_ms: number;
  event_timestamp_iso: string;
  message_id: string | null;
  mg_send_id: string;
  url: string | null;
  reason: string | null;
  tags: string | null;
  client_info: string | null;
  geolocation: string | null;
  user_variables: string | null;
  raw_payload: string;
};

// Normalize a raw Mailgun event into our persisted row shape. Returns null
// if the event lacks the bare minimum identifiers we need (id, event,
// timestamp, recipient) — defensively rejecting malformed events without
// crashing the pull.
export function normalizeEvent(
  raw: MailgunEventRaw,
  domain: string,
  sendID: string,
): NormalizedEvent | null {
  if (!raw || typeof raw !== 'object') return null;
  if (!raw.id || !raw.event || typeof raw.timestamp !== 'number') return null;
  const recipient = (raw.recipient ?? '').toLowerCase();
  if (!recipient) return null;
  const atIdx = recipient.lastIndexOf('@');
  const recipientDomain = atIdx >= 0 ? recipient.slice(atIdx + 1) : '';
  const tsMs = Math.floor(raw.timestamp * 1000);
  return {
    domain,
    mailgun_event_id: raw.id,
    event: raw.event,
    severity: (raw.severity as string | undefined) ?? null,
    recipient,
    recipient_domain: recipientDomain,
    event_timestamp_ms: tsMs,
    event_timestamp_iso: new Date(tsMs).toISOString(),
    message_id:
      (raw.message?.headers?.['message-id'] as string | undefined) ?? null,
    mg_send_id: sendID,
    url: (raw.url as string | undefined) ?? null,
    reason: (raw.reason as string | undefined) ?? null,
    tags: raw.tags ? JSON.stringify(raw.tags) : null,
    client_info: raw['client-info'] ? JSON.stringify(raw['client-info']) : null,
    geolocation: raw.geolocation ? JSON.stringify(raw.geolocation) : null,
    user_variables: raw['user-variables'] ? JSON.stringify(raw['user-variables']) : null,
    raw_payload: JSON.stringify(raw),
  };
}

// Insert one event into mailgun_events with INSERT OR IGNORE. Returns
// { inserted: true, contact_id } if the row was new, { inserted: false }
// if a row with this mailgun_event_id already existed.
//
// Idempotency is the entire reason the events archive is safe to retry —
// the 6h overlap window we re-fetch on every beat will re-hit events
// we've already archived, and the UNIQUE constraint on mailgun_event_id
// is what makes that a no-op.
//
// Returns contact_id so the caller can decide whether to fire the
// engagement-summary UPSERT (only fires when a row was actually new).
export async function insertEventIfNew(
  db: D1Database,
  ev: NormalizedEvent,
): Promise<{ inserted: boolean; contact_id: string | null }> {
  const id = newMailgunEvent();
  const now = nowISO();
  // SQLite's changes() reports rows affected by the LAST statement on this
  // connection. INSERT OR IGNORE returns 0 changes when the UNIQUE
  // constraint is violated (i.e. duplicate event), 1 when the row was
  // actually new. D1's .meta.changes carries the same value.
  const insertResult = await db
    .prepare(
      `INSERT OR IGNORE INTO mailgun_events
         (id, domain, mailgun_event_id, event, severity, recipient, recipient_domain,
          event_timestamp_ms, event_timestamp_iso, message_id, mg_send_id, contact_id,
          url, reason, tags, client_info, geolocation, user_variables, raw_payload, created_at)
       VALUES (
         ?, ?, ?, ?, ?, ?, ?,
         ?, ?, ?, ?,
         (SELECT id FROM contacts WHERE email = ? LIMIT 1),
         ?, ?, ?, ?, ?, ?, ?, ?
       )`,
    )
    .bind(
      id,
      ev.domain,
      ev.mailgun_event_id,
      ev.event,
      ev.severity,
      ev.recipient,
      ev.recipient_domain,
      ev.event_timestamp_ms,
      ev.event_timestamp_iso,
      ev.message_id,
      ev.mg_send_id,
      ev.recipient,
      ev.url,
      ev.reason,
      ev.tags,
      ev.client_info,
      ev.geolocation,
      ev.user_variables,
      ev.raw_payload,
      now,
    )
    .run();

  const inserted = (insertResult.meta?.changes ?? 0) > 0;
  if (!inserted) return { inserted: false, contact_id: null };

  // Look up the contact_id we just stamped onto the row. Cheaper than a
  // second SELECT against contacts because the row we want is the one
  // we just inserted and the index lookup is single-key.
  const row = await db
    .prepare(`SELECT contact_id FROM mailgun_events WHERE id = ?`)
    .bind(id)
    .first<{ contact_id: string | null }>();
  return { inserted: true, contact_id: row?.contact_id ?? null };
}

// ---------------------------------------------------------------------------
// Engagement summary UPSERT
// ---------------------------------------------------------------------------

// Apply an event to the per-(contact, list) engagement summary. Only
// called when insertEventIfNew returned inserted=true AND we have a
// list_id (i.e. the send is bulk or a list-tied single). Singles with
// list_id=NULL skip this step entirely, preserving the invariant that
// contact_engagement is "engagement scored against a list."
//
// The semantics:
//   - delivered → bump total_delivered + messages_since_last_engagement
//   - opened    → bump total_opens, RESET messages_since_last_engagement
//   - clicked   → bump total_clicks, RESET messages_since_last_engagement
//   - other     → no-op
//
// Idempotency lives one layer up: this is only called when the parent
// INSERT actually inserted a row, so we can't double-count.
export async function applyEventToEngagement(
  db: D1Database,
  contactID: string,
  listID: string,
  eventType: string,
  eventTsMs: number,
): Promise<void> {
  const now = nowISO();
  switch (eventType) {
    case 'delivered':
      await db
        .prepare(
          `INSERT INTO contact_engagement
             (contact_id, list_id, last_delivered_at_ms,
              total_delivered, messages_since_last_engagement, updated_at)
             VALUES (?, ?, ?, 1, 1, ?)
           ON CONFLICT(contact_id, list_id) DO UPDATE SET
             last_delivered_at_ms           = MAX(COALESCE(last_delivered_at_ms, 0), excluded.last_delivered_at_ms),
             total_delivered                = total_delivered + 1,
             messages_since_last_engagement = messages_since_last_engagement + 1,
             updated_at                     = excluded.updated_at`,
        )
        .bind(contactID, listID, eventTsMs, now)
        .run();
      return;
    case 'opened':
      await db
        .prepare(
          `INSERT INTO contact_engagement
             (contact_id, list_id, last_open_at_ms, last_engagement_at_ms,
              total_opens, messages_since_last_engagement, updated_at)
             VALUES (?, ?, ?, ?, 1, 0, ?)
           ON CONFLICT(contact_id, list_id) DO UPDATE SET
             last_open_at_ms                = MAX(COALESCE(last_open_at_ms, 0), excluded.last_open_at_ms),
             last_engagement_at_ms          = MAX(COALESCE(last_engagement_at_ms, 0), excluded.last_open_at_ms),
             total_opens                    = total_opens + 1,
             messages_since_last_engagement = 0,
             updated_at                     = excluded.updated_at`,
        )
        .bind(contactID, listID, eventTsMs, eventTsMs, now)
        .run();
      return;
    case 'clicked':
      await db
        .prepare(
          `INSERT INTO contact_engagement
             (contact_id, list_id, last_click_at_ms, last_engagement_at_ms,
              total_clicks, messages_since_last_engagement, updated_at)
             VALUES (?, ?, ?, ?, 1, 0, ?)
           ON CONFLICT(contact_id, list_id) DO UPDATE SET
             last_click_at_ms               = MAX(COALESCE(last_click_at_ms, 0), excluded.last_click_at_ms),
             last_engagement_at_ms          = MAX(COALESCE(last_engagement_at_ms, 0), excluded.last_click_at_ms),
             total_clicks                   = total_clicks + 1,
             messages_since_last_engagement = 0,
             updated_at                     = excluded.updated_at`,
        )
        .bind(contactID, listID, eventTsMs, eventTsMs, now)
        .run();
      return;
    default:
      return;
  }
}

// Look up the list_id for a send. Returns null for singles that aren't
// list-tied. Cached opportunistically by the caller per-pull (one send
// = one list_id, so we look it up once per pullEventsForOneSend invocation).
export async function getSendListID(db: D1Database, sendID: string): Promise<string | null> {
  const row = await db
    .prepare(`SELECT list_id FROM sends WHERE id = ?`)
    .bind(sendID)
    .first<{ list_id: string | null }>();
  return row?.list_id ?? null;
}
