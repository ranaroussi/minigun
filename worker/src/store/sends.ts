import { newSend } from '../lib/ids';
import { initSendStatsStmt, markSendCompletedForStatsStmt } from './stats';
import {
  NotFoundError,
  Send,
  SendStatus,
  SendSummary,
  SendType,
  UnsubscribeMode,
  nowISO,
} from './types';

export type NewSendParams = {
  type: SendType;
  list_id?: string | null;
  recipient_email?: string | null;
  subject: string;
  from_header: string;
  reply_to?: string | null;
  template_name?: string | null;
  body_md?: string | null;
  body_html?: string | null;
  body_text?: string | null;
  sending_domain: string;
  batch_size?: number;
  throttle_ms?: number;
  test_mode?: boolean;
  // Bulk: cursor (highest subscription_id processed; starts 0). Single: the
  // recipient's own subscription_id when caller passed a list, so runSingle
  // can sign a per-recipient unsub token. 0 = no list, no opt-out link.
  last_subscription_id?: number;
  max_subscription_id?: number | null;
  total_recipients?: number;
  unsubscribe_mode?: UnsubscribeMode;
  unsubscribe_redirect_url?: string | null;
  unsubscribe_external_url?: string | null;
  notify_email?: string | null;
  // ISO-8601 schedule time. When present and in the future the send is
  // parked in 'scheduled' status for the cron dispatcher; otherwise it
  // sends immediately. Already validated/normalized by the route.
  send_at?: string | null;
};

// Hard cap on recipients per batch, enforced regardless of what a caller
// (CLI, MCP, API) requests. Building a batch's per-recipient Mailgun variables
// (HMAC-signed unsub tokens + merge vars) is CPU-bound and linear in size;
// too large an invocation trips the Workers CPU limit, dies before recording
// an outcome, and stalls the send. 200 balances throughput against that risk;
// if an isolate ever chokes at 200, the watchdog self-heals down to
// SAFE_BATCH_FLOOR. Used both as the default and the ceiling.
export const MAX_BATCH_SIZE = 200;

// Floor the watchdog reduces a stalled send's batch_size down to. 100 has
// drained large sends cleanly and repeatedly, so it is the proven-safe target;
// kept distinct from MAX_BATCH_SIZE so the creation cap can be raised without
// lowering the self-heal safety net.
export const SAFE_BATCH_FLOOR = 100;

// Ceiling on the inter-batch sleep, enforced worker-side so the operative
// pace is governed here rather than by whatever a caller hard-codes. Callers
// may request a shorter delay; anything larger is capped to this.
export const MAX_THROTTLE_MS = 500;

export async function createSend(db: D1Database, p: NewSendParams): Promise<Send> {
  const id = newSend();
  const now = nowISO();
  const requested = p.batch_size && p.batch_size > 0 ? p.batch_size : MAX_BATCH_SIZE;
  const batchSize = Math.min(requested, MAX_BATCH_SIZE);
  const reqThrottle =
    p.throttle_ms !== undefined && p.throttle_ms >= 0 ? p.throttle_ms : MAX_THROTTLE_MS;
  const throttleMs = Math.min(reqThrottle, MAX_THROTTLE_MS);
  const mode = p.unsubscribe_mode || 'local';
  // Park the send only when send_at is genuinely in the future; a past or
  // absent value sends now (status 'queued', send_at NULL).
  let status: SendStatus = 'queued';
  let sendAt: string | null = null;
  if (p.send_at && new Date(p.send_at).getTime() > Date.now()) {
    status = 'scheduled';
    sendAt = new Date(p.send_at).toISOString();
  }
  const insertSend = db
    .prepare(
      `INSERT INTO sends (
        id, type, list_id, recipient_email, subject, from_header, reply_to, template_name,
        body_md, body_html, body_text, sending_domain,
        status, batch_size, throttle_ms, test_mode,
        last_subscription_id, max_subscription_id, total_recipients,
        unsubscribe_mode, unsubscribe_redirect_url, unsubscribe_external_url,
        notify_email, send_at, created_at, updated_at
      ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
    )
    .bind(
      id,
      p.type,
      p.list_id ?? null,
      p.recipient_email ?? null,
      p.subject,
      p.from_header,
      p.reply_to ?? null,
      p.template_name ?? null,
      p.body_md ?? null,
      p.body_html ?? null,
      p.body_text ?? null,
      p.sending_domain,
      status,
      batchSize,
      throttleMs,
      p.test_mode ? 1 : 0,
      p.last_subscription_id ?? 0,
      p.max_subscription_id ?? null,
      p.total_recipients ?? 0,
      mode,
      p.unsubscribe_redirect_url ?? null,
      p.unsubscribe_external_url ?? null,
      p.notify_email ?? null,
      sendAt,
      now,
      now,
    );
  await db.batch([insertSend, initSendStatsStmt(db, id)]);
  return getSend(db, id);
}

const SEND_COLUMNS = `id, type, list_id, recipient_email, subject, from_header, reply_to, template_name,
       body_md, body_html, body_text, sending_domain,
       status, batch_size, throttle_ms, test_mode,
       last_subscription_id, max_subscription_id, total_recipients,
       unsubscribe_mode, unsubscribe_redirect_url, unsubscribe_external_url,
       notify_email, send_at, last_error, created_at, updated_at, completed_at`;

export async function getSend(db: D1Database, id: string): Promise<Send> {
  const row = await db
    .prepare(`SELECT ${SEND_COLUMNS} FROM sends WHERE id = ?`)
    .bind(id)
    .first<Send>();
  if (!row) throw new NotFoundError();
  return row;
}

export async function updateSendStatus(
  db: D1Database,
  id: string,
  status: SendStatus,
  lastErr: string | null,
): Promise<void> {
  const now = nowISO();
  const isTerminal = status === 'completed' || status === 'failed' || status === 'cancelled';
  const updateSend = db
    .prepare(
      `UPDATE sends SET status = ?, last_error = ?, updated_at = ?,
              completed_at = COALESCE(?, completed_at) WHERE id = ?`,
    )
    .bind(status, lastErr, now, isTerminal ? now : null, id);
  if (isTerminal) {
    await db.batch([updateSend, markSendCompletedForStatsStmt(db, id)]);
    return;
  }
  await updateSend.run();
}

export async function advanceSendCursor(
  db: D1Database,
  id: string,
  lastSubID: number,
): Promise<void> {
  const now = nowISO();
  await db
    .prepare('UPDATE sends SET last_subscription_id = ?, updated_at = ? WHERE id = ?')
    .bind(lastSubID, now, id)
    .run();
}

// setSendAudience freezes a bulk send's recipient set (upper subscription-id
// bound + resolved count). Immediate sends capture this at creation; a
// scheduled send defers it to dispatch so everyone subscribed up to go-time
// is included.
export async function setSendAudience(
  db: D1Database,
  id: string,
  maxSubID: number,
  total: number,
): Promise<void> {
  const now = nowISO();
  await db
    .prepare('UPDATE sends SET max_subscription_id = ?, total_recipients = ?, updated_at = ? WHERE id = ?')
    .bind(maxSubID, total, now, id)
    .run();
}

export async function listRunningSends(db: D1Database): Promise<Send[]> {
  const { results } = await db
    .prepare(
      `SELECT ${SEND_COLUMNS} FROM sends
        WHERE status IN ('queued', 'running')
        ORDER BY created_at ASC`,
    )
    .all<Send>();
  return results;
}

// Reports whether any send is currently mid-flight (queued or running).
// Used to pause the engagement events-pull while a send is draining: the
// per-tick pull and the send's step loop compete for the same cron/CPU
// budget, and starving the send watchdog is the worse failure. Cheap:
// a single indexed COUNT with an early LIMIT.
export async function hasActiveSend(db: D1Database): Promise<boolean> {
  const row = await db
    .prepare(
      `SELECT 1 AS n FROM sends WHERE status IN ('queued', 'running') LIMIT 1`,
    )
    .first<{ n: number }>();
  return row != null;
}

// Reclaim batches left 'in_flight' past the stale window. A batch is flipped
// to 'in_flight' immediately before the Mailgun call and to succeeded/failed
// right after; a Worker invocation cannot run for minutes, so an in_flight
// batch older than staleBefore is orphaned (its invocation died, e.g.
// exceededCpu, before recording an outcome). listStuckSends deliberately
// skips any send that still has an in_flight batch, so an orphan would wedge
// the send forever. Marking the orphan 'failed' frees the send to resume from
// its cursor and re-send that range. Trade-off: if the worker died AFTER
// Mailgun accepted but BEFORE this row was written (a sub-second window),
// those recipients get a duplicate. That narrow risk beats a permanently
// stalled send. Returns the number of batches reclaimed.
export async function reclaimStuckBatches(
  db: D1Database,
  staleBefore: string,
): Promise<number> {
  const res = await db
    .prepare(
      `UPDATE send_batches
          SET status = 'failed',
              mailgun_response = 'reclaimed: in_flight past stale window (orphaned invocation)',
              updated_at = ?
        WHERE status = 'in_flight' AND updated_at < ?`,
    )
    .bind(nowISO(), staleBefore)
    .run();
  return (res.meta as { changes?: number } | undefined)?.changes ?? 0;
}

// Halve a stalled send's batch_size, never below `floor`. Called by the
// watchdog when a running send's step chain has died without progress (its
// batch was too large to build under the CPU limit). Halving on each stale
// sweep walks an oversized send (e.g. a legacy 500) down toward the safe
// floor until step() completes, so it self-heals instead of needing a manual
// batch_size fix. The batch_size guard makes it a no-op once at/under floor,
// and bumping updated_at paces reductions to one per stale window. Returns the
// number of rows changed (1 when reduced, 0 when already at/under floor).
export async function reduceStuckSendBatchSize(
  db: D1Database,
  id: string,
  floor: number,
): Promise<number> {
  const res = await db
    .prepare(
      `UPDATE sends
          SET batch_size = MAX(?, batch_size / 2),
              updated_at = ?
        WHERE id = ? AND status IN ('queued', 'running') AND batch_size > ?`,
    )
    .bind(floor, nowISO(), id, floor)
    .run();
  return (res.meta as { changes?: number } | undefined)?.changes ?? 0;
}

export async function listStuckSends(
  db: D1Database,
  staleBefore: string,
): Promise<Send[]> {
  const { results } = await db
    .prepare(
      `SELECT ${SEND_COLUMNS} FROM sends
        WHERE status IN ('queued', 'running') AND updated_at < ?
          AND id NOT IN (SELECT send_id FROM send_batches WHERE status = 'in_flight')
        ORDER BY updated_at ASC`,
    )
    .bind(staleBefore)
    .all<Send>();
  return results;
}

// Scheduled sends whose send_at has arrived (send_at <= now), oldest first.
// send_at and the now bound are both fixed-format ISO-8601 UTC, so the
// lexical string comparison is exactly chronological.
export async function listDueScheduledSends(db: D1Database, limit = 100): Promise<Send[]> {
  const now = nowISO();
  const { results } = await db
    .prepare(
      `SELECT ${SEND_COLUMNS} FROM sends
        WHERE status = 'scheduled' AND send_at IS NOT NULL AND send_at <= ?
        ORDER BY send_at ASC
        LIMIT ?`,
    )
    .bind(now, limit)
    .all<Send>();
  return results;
}

// Cancel a send, but only from the pre-dispatch states ('scheduled' or
// 'queued'). The guarded WHERE makes this race-safe against the dispatcher:
// returns false (zero rows) if the send already started.
export async function cancelScheduledSend(db: D1Database, id: string): Promise<boolean> {
  const now = nowISO();
  const res = await db
    .prepare(
      `UPDATE sends SET status = 'cancelled', updated_at = ?, completed_at = ?
        WHERE id = ? AND status IN ('scheduled', 'queued')`,
    )
    .bind(now, now, id)
    .run();
  const changes = (res.meta as { changes?: number } | undefined)?.changes ?? 0;
  if (changes === 0) return false;
  await markSendCompletedForStatsStmt(db, id).run();
  return true;
}

export async function hasInFlightBatch(db: D1Database, sendID: string): Promise<boolean> {
  const row = await db
    .prepare(`SELECT COUNT(*) AS n FROM send_batches WHERE send_id = ? AND status = 'in_flight'`)
    .bind(sendID)
    .first<{ n: number }>();
  return (row?.n ?? 0) > 0;
}

export async function listSends(
  db: D1Database,
  afterCreatedAt: string,
  afterID: string,
  limit: number,
  listID = '',
): Promise<SendSummary[]> {
  const { results } = await db
    .prepare(
      `SELECT id, type, list_id, recipient_email, subject, status, total_recipients,
              send_at, created_at, updated_at, completed_at
         FROM sends
        WHERE (? = '' OR created_at < ? OR (created_at = ? AND id < ?))
          AND (? = '' OR list_id = ?)
        ORDER BY created_at DESC, id DESC
        LIMIT ?`,
    )
    .bind(afterCreatedAt, afterCreatedAt, afterCreatedAt, afterID, listID, listID, limit)
    .all<SendSummary>();
  return results;
}
