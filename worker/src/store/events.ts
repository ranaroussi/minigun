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

// A row of contact_message_engagement — per-(send, contact) message
// detail (Phase 6). Timestamps are epoch SECONDS.
export type MessageEngagement = {
  send_id: string;
  contact_id: string;
  list_id: string | null;
  sent_at: number | null;
  delivered_at: number | null;
  first_open_at: number | null;
  last_open_at: number | null;
  total_opens: number;
  first_click_at: number | null;
  last_click_at: number | null;
  total_clicks: number;
  failed: number;
  failed_at: number | null;
  failure_severity: string | null;
  failure_reason: string | null;
  complained_at: number | null;
  unsubscribed_at: number | null;
  replied_at: number | null;
  updated_at: number;
};

// ---------------------------------------------------------------------------
// Cron-helper queries
// ---------------------------------------------------------------------------

// Selects non-frozen, non-test sends with a sending_domain that are DUE
// for a pull right now. The due predicate below mirrors nextDueAt() in
// send/events_pull.ts exactly (the worker re-checks it as a safety net):
//
//   - Burst phase (events_pulls_count < 4): due when
//     created_at + BURST_OFFSETS_MS[count] <= now. Offsets are
//     [0, +1h, +6h, +24h] = [0, 3600000, 21600000, 86400000] ms.
//   - Daily phase (count >= 4): due when last_pulled + 24h <= now, OR
//     when the next daily beat would fall past the archive window
//     (last_pulled + 24h > created + maxAge) — that send still needs one
//     final pull to flip events_archive_complete = 1, so it counts as due.
//
// Filtering due-ness HERE rather than after a LIMIT is critical: the
// queue is ordered by events_last_pulled_at_ms ASC, and a large backlog
// of not-yet-due daily sends (all pulled within the last day) would
// otherwise occupy the entire LIMIT window every tick and starve a fresh
// send waiting on its +1h/+6h burst beat (it sorts behind them because
// it was pulled more recently). We deliberately do NOT exclude sends past
// the age window — they need that final freeze pull.
//
// nowMs is "now"; maxAgeMs is the archive window (ARCHIVE_MAX_AGE_MS).
export async function listDueEventPulls(
  db: D1Database,
  nowMs: number,
  maxAgeMs: number,
  limit: number,
): Promise<DueEventPullRow[]> {
  const DAILY_MS = 24 * 60 * 60 * 1000;
  const { results } = await db
    .prepare(
      `WITH candidates AS (
         SELECT
           id                            AS send_id,
           sending_domain                AS from_domain,
           CAST(strftime('%s', created_at) AS INTEGER) * 1000 AS created_at_ms,
           events_pulls_count,
           events_last_pulled_at_ms,
           events_last_pulled_through_ms
         FROM sends
         WHERE events_archive_complete = 0
           AND test_mode = 0
           AND type = 'bulk'
           AND status IN ('completed', 'failed', 'cancelled', 'running')
           AND sending_domain != ''
       )
       SELECT * FROM candidates
       WHERE (
           events_pulls_count < 4
           AND created_at_ms + (CASE events_pulls_count
                 WHEN 0 THEN 0
                 WHEN 1 THEN 3600000
                 WHEN 2 THEN 21600000
                 ELSE 86400000 END) <= ?1
         )
         OR (
           events_pulls_count >= 4
           AND (
                 COALESCE(events_last_pulled_at_ms, 0) + ?2 <= ?1
              OR COALESCE(events_last_pulled_at_ms, 0) + ?2 > created_at_ms + ?3
           )
         )
       ORDER BY COALESCE(events_last_pulled_at_ms, 0) ASC
       LIMIT ?4`,
    )
    .bind(nowMs, DAILY_MS, maxAgeMs, limit > 0 ? limit : 25)
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

// Normalized payload the pull loop folds directly into the two
// engagement rollups. There is no raw event ledger, so this is never
// persisted — it's a transient carrier between normalizeEvent and the
// applyEventTo* functions, carrying only the fields the rollups consume.
type NormalizedEvent = {
  event: string;
  severity: string | null;
  recipient: string;
  event_timestamp_ms: number;
  reason: string | null;
  // Clicked link (only present on "clicked" events). Folded into
  // contact_message_clicks; canonicalization happens at apply time.
  url: string;
};

// Normalize a raw Mailgun event into the rollup-ready shape. Returns null
// if the event lacks the bare minimum identifiers we need (id, event,
// timestamp, recipient) — defensively rejecting malformed events without
// crashing the pull.
export function normalizeEvent(raw: MailgunEventRaw): NormalizedEvent | null {
  if (!raw || typeof raw !== 'object') return null;
  if (!raw.id || !raw.event || typeof raw.timestamp !== 'number') return null;
  const recipient = (raw.recipient ?? '').toLowerCase();
  if (!recipient) return null;
  return {
    event: raw.event,
    severity: (raw.severity as string | undefined) ?? null,
    recipient,
    event_timestamp_ms: Math.floor(raw.timestamp * 1000),
    reason: (raw.reason as string | undefined) ?? null,
    url: (raw.url as string | undefined) ?? '',
  };
}

// Resolve a recipient email to its contact_id, or null when no contact
// matches. Mailgun can report events for addresses we never stored
// (e.g. forwarded mail); those simply don't move any rollup.
export async function lookupContactIDByEmail(
  db: D1Database,
  email: string,
): Promise<string | null> {
  const row = await db
    .prepare(`SELECT id FROM contacts WHERE email = ? LIMIT 1`)
    .bind(email.toLowerCase())
    .first<{ id: string }>();
  return row?.id ?? null;
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
// Counters increment per call; the incremental watermark (each pull
// begins strictly after the previous pull's highest event timestamp)
// ensures each event is applied exactly once.
export function engagementStmt(
  db: D1Database,
  contactID: string,
  listID: string,
  eventType: string,
  eventTsMs: number,
): D1PreparedStatement | null {
  const now = nowISO();
  switch (eventType) {
    case 'delivered':
      // Phase 5 hardening: messages_since_last_engagement only
      // increments when the delivered event's timestamp is NEWER than
      // the contact's last engagement. Otherwise a late `delivered` for
      // an already-opened message would falsely inflate dormancy and
      // bias prune-by-count toward false positives.
      return db
        .prepare(
          `INSERT INTO contact_engagement
             (contact_id, list_id, last_delivered_at_ms,
              total_delivered, messages_since_last_engagement, updated_at)
             VALUES (?, ?, ?, 1, 1, ?)
           ON CONFLICT(contact_id, list_id) DO UPDATE SET
             last_delivered_at_ms           = MAX(COALESCE(last_delivered_at_ms, 0), excluded.last_delivered_at_ms),
             total_delivered                = total_delivered + 1,
             messages_since_last_engagement = CASE
               WHEN excluded.last_delivered_at_ms > COALESCE(last_engagement_at_ms, 0)
               THEN messages_since_last_engagement + 1
               ELSE messages_since_last_engagement
             END,
             updated_at                     = excluded.updated_at`,
        )
        .bind(contactID, listID, eventTsMs, now);
    case 'opened':
      return db
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
        .bind(contactID, listID, eventTsMs, eventTsMs, now);
    case 'clicked':
      return db
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
        .bind(contactID, listID, eventTsMs, eventTsMs, now);
    default:
      return null;
  }
}

export async function applyEventToEngagement(
  db: D1Database,
  contactID: string,
  listID: string,
  eventType: string,
  eventTsMs: number,
): Promise<void> {
  const stmt = engagementStmt(db, contactID, listID, eventType, eventTsMs);
  if (stmt) await stmt.run();
}

// Apply one event to the per-(send, contact) detail row in
// contact_message_engagement. eventTsSec is epoch SECONDS (caller
// converts from ms). listID may be null for list-less singles.
// severity/reason are only consulted for failures.
//
// Counters increment per call; the incremental watermark ensures each
// event is applied exactly once. Timestamp fields use MIN/MAX so
// out-of-order arrival within a single pull converges.
export function messageEngagementStmt(
  db: D1Database,
  sendID: string,
  contactID: string,
  listID: string | null,
  eventType: string,
  eventTsSec: number,
  severity: string | null,
  reason: string | null,
): D1PreparedStatement | null {
  const now = Math.floor(Date.now() / 1000);
  switch (eventType) {
    case 'accepted':
      return db
        .prepare(
          `INSERT INTO contact_message_engagement (send_id, contact_id, list_id, sent_at, updated_at)
           VALUES (?, ?, ?, ?, ?)
           ON CONFLICT(send_id, contact_id) DO UPDATE SET
             sent_at    = MIN(COALESCE(sent_at, excluded.sent_at), excluded.sent_at),
             updated_at = excluded.updated_at`,
        )
        .bind(sendID, contactID, listID, eventTsSec, now);
    case 'delivered':
      return db
        .prepare(
          `INSERT INTO contact_message_engagement (send_id, contact_id, list_id, delivered_at, updated_at)
           VALUES (?, ?, ?, ?, ?)
           ON CONFLICT(send_id, contact_id) DO UPDATE SET
             delivered_at = MIN(COALESCE(delivered_at, excluded.delivered_at), excluded.delivered_at),
             updated_at   = excluded.updated_at`,
        )
        .bind(sendID, contactID, listID, eventTsSec, now);
    case 'opened':
      return db
        .prepare(
          `INSERT INTO contact_message_engagement (send_id, contact_id, list_id, first_open_at, last_open_at, total_opens, updated_at)
           VALUES (?, ?, ?, ?, ?, 1, ?)
           ON CONFLICT(send_id, contact_id) DO UPDATE SET
             first_open_at = MIN(COALESCE(first_open_at, excluded.first_open_at), excluded.first_open_at),
             last_open_at  = MAX(COALESCE(last_open_at, 0), excluded.last_open_at),
             total_opens   = total_opens + 1,
             updated_at    = excluded.updated_at`,
        )
        .bind(sendID, contactID, listID, eventTsSec, eventTsSec, now);
    case 'clicked':
      return db
        .prepare(
          `INSERT INTO contact_message_engagement (send_id, contact_id, list_id, first_click_at, last_click_at, total_clicks, updated_at)
           VALUES (?, ?, ?, ?, ?, 1, ?)
           ON CONFLICT(send_id, contact_id) DO UPDATE SET
             first_click_at = MIN(COALESCE(first_click_at, excluded.first_click_at), excluded.first_click_at),
             last_click_at  = MAX(COALESCE(last_click_at, 0), excluded.last_click_at),
             total_clicks   = total_clicks + 1,
             updated_at     = excluded.updated_at`,
        )
        .bind(sendID, contactID, listID, eventTsSec, eventTsSec, now);
    case 'failed':
    case 'rejected':
      return db
        .prepare(
          `INSERT INTO contact_message_engagement (send_id, contact_id, list_id, failed, failed_at, failure_severity, failure_reason, updated_at)
           VALUES (?, ?, ?, 1, ?, ?, ?, ?)
           ON CONFLICT(send_id, contact_id) DO UPDATE SET
             failed           = 1,
             failed_at        = MAX(COALESCE(failed_at, 0), excluded.failed_at),
             failure_severity = excluded.failure_severity,
             failure_reason   = excluded.failure_reason,
             updated_at       = excluded.updated_at`,
        )
        .bind(sendID, contactID, listID, eventTsSec, severity, reason, now);
    case 'complained':
      return db
        .prepare(
          `INSERT INTO contact_message_engagement (send_id, contact_id, list_id, complained_at, updated_at)
           VALUES (?, ?, ?, ?, ?)
           ON CONFLICT(send_id, contact_id) DO UPDATE SET
             complained_at = MAX(COALESCE(complained_at, 0), excluded.complained_at),
             updated_at    = excluded.updated_at`,
        )
        .bind(sendID, contactID, listID, eventTsSec, now);
    case 'unsubscribed':
      return db
        .prepare(
          `INSERT INTO contact_message_engagement (send_id, contact_id, list_id, unsubscribed_at, updated_at)
           VALUES (?, ?, ?, ?, ?)
           ON CONFLICT(send_id, contact_id) DO UPDATE SET
             unsubscribed_at = MAX(COALESCE(unsubscribed_at, 0), excluded.unsubscribed_at),
             updated_at      = excluded.updated_at`,
        )
        .bind(sendID, contactID, listID, eventTsSec, now);
    default:
      return null;
  }
}

export async function applyEventToMessageEngagement(
  db: D1Database,
  sendID: string,
  contactID: string,
  listID: string | null,
  eventType: string,
  eventTsSec: number,
  severity: string | null,
  reason: string | null,
): Promise<void> {
  const stmt = messageEngagementStmt(db, sendID, contactID, listID, eventType, eventTsSec, severity, reason);
  if (stmt) await stmt.run();
}

// canonicalizeClickURL normalizes a clicked link so the per-URL rollup
// keys on the destination rather than per-recipient link noise: trim,
// lowercase scheme+host (path case preserved), drop query string and
// fragment. On a parse failure it returns the trimmed input so a
// malformed link still aggregates deterministically. The URL constructor
// already lowercases protocol+host and renders a bare host with a
// trailing slash, matching the Go canonicalizer.
export function canonicalizeClickURL(raw: string): string {
  const trimmed = (raw ?? '').trim();
  if (!trimmed) return '';
  try {
    const u = new URL(trimmed);
    u.search = '';
    u.hash = '';
    return u.toString();
  } catch {
    return trimmed;
  }
}

// applyClickToURL folds one "clicked" event into the per-URL rollup
// (contact_message_clicks). url is canonicalized here; an empty/blank url
// is a no-op (cme.total_clicks still counts it). eventTsSec is epoch
// SECONDS. Same accepted crash/retry counter drift as the other Apply*.
export function clickStmt(
  db: D1Database,
  sendID: string,
  contactID: string,
  listID: string | null,
  rawURL: string,
  eventTsSec: number,
): D1PreparedStatement | null {
  const clickURL = canonicalizeClickURL(rawURL);
  if (!clickURL) return null;
  const now = Math.floor(Date.now() / 1000);
  return db
    .prepare(
      `INSERT INTO contact_message_clicks
         (send_id, contact_id, list_id, url, first_click_at, last_click_at, total_clicks, updated_at)
         VALUES (?, ?, ?, ?, ?, ?, 1, ?)
       ON CONFLICT(send_id, contact_id, url) DO UPDATE SET
         first_click_at = MIN(COALESCE(first_click_at, excluded.first_click_at), excluded.first_click_at),
         last_click_at  = MAX(COALESCE(last_click_at, 0), excluded.last_click_at),
         total_clicks   = total_clicks + 1,
         list_id        = COALESCE(list_id, excluded.list_id),
         updated_at     = excluded.updated_at`,
    )
    .bind(sendID, contactID, listID, clickURL, eventTsSec, eventTsSec, now);
}

export async function applyClickToURL(
  db: D1Database,
  sendID: string,
  contactID: string,
  listID: string | null,
  rawURL: string,
  eventTsSec: number,
): Promise<void> {
  const stmt = clickStmt(db, sendID, contactID, listID, rawURL, eventTsSec);
  if (stmt) await stmt.run();
}

// Resolve many recipient emails to contact_ids in one query (chunked to
// stay under SQLite's bound-variable cap). Used by the batched pull loop
// so a page of events triggers one read instead of one lookup per event.
export async function lookupContactIDsByEmails(
  db: D1Database,
  emails: string[],
): Promise<Map<string, string>> {
  const out = new Map<string, string>();
  const unique = [...new Set(emails.map((e) => e.toLowerCase()))];
  // D1 caps bound parameters at 100 per query, so chunk the IN-list well
  // under that.
  const CHUNK = 90;
  for (let i = 0; i < unique.length; i += CHUNK) {
    const slice = unique.slice(i, i + CHUNK);
    const placeholders = slice.map(() => '?').join(',');
    const { results } = await db
      .prepare(`SELECT id, email FROM contacts WHERE email IN (${placeholders})`)
      .bind(...slice)
      .all<{ id: string; email: string }>();
    for (const r of results ?? []) out.set(r.email.toLowerCase(), r.id);
  }
  return out;
}

// Advance only the watermark for a multi-beat pull that hasn't caught up
// yet. Unlike recordEventPullProgress this does NOT bump events_pulls_count
// (which tracks scheduled burst beats, not cron ticks) and never freezes —
// it just durably checkpoints forward progress so a CPU-killed beat
// resumes after the last fully-processed page instead of restarting.
//
// Deliberately leaves events_last_pulled_at_ms untouched: the due-set is
// ordered by that column ASC, so advancing it mid-drain would push a
// half-finished large send to the back of the queue behind never-pulled
// sends, starving it. It stays put (and thus keeps getting picked each
// tick) until the catch-up recordEventPullProgress stamps it.
export async function checkpointEventPullThrough(
  db: D1Database,
  sendID: string,
  args: { last_pulled_through_ms: number; inserted: number },
): Promise<void> {
  await db
    .prepare(
      `UPDATE sends SET
         events_last_pulled_through_ms = ?,
         events_archive_count          = events_archive_count + ?,
         events_last_pull_error        = NULL,
         updated_at                    = ?
       WHERE id = ?`,
    )
    .bind(args.last_pulled_through_ms, args.inserted, nowISO(), sendID)
    .run();
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

// ---------------------------------------------------------------------------
// Read endpoints
// ---------------------------------------------------------------------------

export type ListSendRecipientsParams = {
  sendID: string;
  afterContactID?: string;
  limit?: number;
};

// listSendRecipients returns one page of per-recipient message
// engagement rows for a send, ordered by contact_id ASC. Keyset
// paginated on contact_id (unique within a send via the composite PK).
export async function listSendRecipients(
  db: D1Database,
  p: ListSendRecipientsParams,
): Promise<MessageEngagement[]> {
  const limit = Math.max(1, Math.min(p.limit ?? 100, 500));
  const clauses: string[] = ['send_id = ?'];
  const args: unknown[] = [p.sendID];
  if (p.afterContactID) {
    clauses.push('contact_id > ?');
    args.push(p.afterContactID);
  }
  args.push(limit);
  const sql = `
    SELECT send_id, contact_id, list_id, sent_at, delivered_at,
           first_open_at, last_open_at, total_opens,
           first_click_at, last_click_at, total_clicks,
           failed, failed_at, failure_severity, failure_reason,
           complained_at, unsubscribed_at, replied_at, updated_at
    FROM contact_message_engagement
    WHERE ${clauses.join(' AND ')}
    ORDER BY contact_id ASC
    LIMIT ?`;
  const { results } = await db.prepare(sql).bind(...args).all<MessageEngagement>();
  return results ?? [];
}

// MessageClick mirrors a row of contact_message_clicks — the per-URL
// click rollup for a (send, contact). Timestamps are epoch SECONDS.
export type MessageClick = {
  send_id: string;
  contact_id: string;
  list_id: string | null;
  url: string;
  first_click_at: number | null;
  last_click_at: number | null;
  total_clicks: number;
  updated_at: number;
};

export type ListSendClicksParams = {
  sendID: string;
  afterContactID?: string;
  afterURL?: string;
  limit?: number;
};

// listSendClicks returns one page of per-URL click rows for a send,
// ordered by (contact_id, url) ASC. Keyset-paginated on that composite
// (unique within a send via the PK).
export async function listSendClicks(
  db: D1Database,
  p: ListSendClicksParams,
): Promise<MessageClick[]> {
  const limit = Math.max(1, Math.min(p.limit ?? 100, 500));
  const clauses: string[] = ['send_id = ?'];
  const args: unknown[] = [p.sendID];
  if (p.afterContactID) {
    clauses.push('(contact_id > ? OR (contact_id = ? AND url > ?))');
    args.push(p.afterContactID, p.afterContactID, p.afterURL ?? '');
  }
  args.push(limit);
  const sql = `
    SELECT send_id, contact_id, list_id, url,
           first_click_at, last_click_at, total_clicks, updated_at
    FROM contact_message_clicks
    WHERE ${clauses.join(' AND ')}
    ORDER BY contact_id ASC, url ASC
    LIMIT ?`;
  const { results } = await db.prepare(sql).bind(...args).all<MessageClick>();
  return results ?? [];
}

// ContactEngagement mirrors a row of contact_engagement, normalized for
// JSON wire output (nullable big-ints stay as number|null).
export type ContactEngagement = {
  contact_id: string;
  list_id: string;
  last_delivered_at_ms: number | null;
  last_open_at_ms: number | null;
  last_click_at_ms: number | null;
  last_engagement_at_ms: number | null;
  total_delivered: number;
  total_opens: number;
  total_clicks: number;
  messages_since_last_engagement: number;
  updated_at: string;
};

// listContactEngagement returns per-list engagement rows for one contact.
// listID="" returns one row per list the contact has engaged with;
// otherwise narrows to the (contact, list) singleton.
export async function listContactEngagement(
  db: D1Database,
  contactID: string,
  listID: string,
): Promise<ContactEngagement[]> {
  const clauses: string[] = ['contact_id = ?'];
  const args: unknown[] = [contactID];
  if (listID) {
    clauses.push('list_id = ?');
    args.push(listID);
  }
  const sql = `
    SELECT contact_id, list_id,
           last_delivered_at_ms, last_open_at_ms, last_click_at_ms, last_engagement_at_ms,
           total_delivered, total_opens, total_clicks,
           messages_since_last_engagement, updated_at
    FROM contact_engagement
    WHERE ${clauses.join(' AND ')}
    ORDER BY (last_engagement_at_ms IS NULL) ASC, last_engagement_at_ms DESC,
             (last_delivered_at_ms IS NULL) ASC, last_delivered_at_ms DESC`;
  const { results } = await db.prepare(sql).bind(...args).all<ContactEngagement>();
  return results ?? [];
}

// resolveContactID accepts a contact id (c_*) or email and returns the
// canonical contact_id, or null when no contact matches.
export async function resolveContactID(
  db: D1Database,
  idOrEmail: string,
): Promise<string | null> {
  const key = idOrEmail.trim();
  if (!key) return null;
  if (key.startsWith('c_')) {
    const row = await db
      .prepare(`SELECT id FROM contacts WHERE id = ?`)
      .bind(key)
      .first<{ id: string }>();
    return row?.id ?? null;
  }
  const row = await db
    .prepare(`SELECT id FROM contacts WHERE email = ?`)
    .bind(key.toLowerCase())
    .first<{ id: string }>();
  return row?.id ?? null;
}
