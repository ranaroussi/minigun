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
  batch_size?: number;
  throttle_ms?: number;
  max_subscription_id?: number | null;
  total_recipients?: number;
  unsubscribe_mode?: UnsubscribeMode;
  unsubscribe_redirect_url?: string | null;
  unsubscribe_external_url?: string | null;
  notify_email?: string | null;
};

export async function createSend(db: D1Database, p: NewSendParams): Promise<Send> {
  const id = newSend();
  const now = nowISO();
  const batchSize = p.batch_size && p.batch_size > 0 ? p.batch_size : 500;
  const throttleMs = p.throttle_ms !== undefined && p.throttle_ms >= 0 ? p.throttle_ms : 1000;
  const mode = p.unsubscribe_mode || 'local';
  const insertSend = db
    .prepare(
      `INSERT INTO sends (
        id, type, list_id, recipient_email, subject, from_header, reply_to, template_name,
        body_md, body_html, body_text,
        status, batch_size, throttle_ms,
        last_subscription_id, max_subscription_id, total_recipients,
        unsubscribe_mode, unsubscribe_redirect_url, unsubscribe_external_url,
        notify_email, created_at, updated_at
      ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?, ?, ?, ?)`,
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
      'queued',
      batchSize,
      throttleMs,
      p.max_subscription_id ?? null,
      p.total_recipients ?? 0,
      mode,
      p.unsubscribe_redirect_url ?? null,
      p.unsubscribe_external_url ?? null,
      p.notify_email ?? null,
      now,
      now,
    );
  await db.batch([insertSend, initSendStatsStmt(db, id)]);
  return getSend(db, id);
}

const SEND_COLUMNS = `id, type, list_id, recipient_email, subject, from_header, reply_to, template_name,
       body_md, body_html, body_text,
       status, batch_size, throttle_ms,
       last_subscription_id, max_subscription_id, total_recipients,
       unsubscribe_mode, unsubscribe_redirect_url, unsubscribe_external_url,
       notify_email, last_error, created_at, updated_at, completed_at`;

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

export async function listStuckRunningSends(
  db: D1Database,
  staleBefore: string,
): Promise<Send[]> {
  const { results } = await db
    .prepare(
      `SELECT ${SEND_COLUMNS} FROM sends
        WHERE status = 'running' AND updated_at < ?
          AND id NOT IN (SELECT send_id FROM send_batches WHERE status = 'in_flight')
        ORDER BY updated_at ASC`,
    )
    .bind(staleBefore)
    .all<Send>();
  return results;
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
): Promise<SendSummary[]> {
  const { results } = await db
    .prepare(
      `SELECT id, type, list_id, recipient_email, subject, status, total_recipients,
              created_at, updated_at, completed_at
         FROM sends
        WHERE (? = '' OR created_at < ? OR (created_at = ? AND id < ?))
        ORDER BY created_at DESC, id DESC
        LIMIT ?`,
    )
    .bind(afterCreatedAt, afterCreatedAt, afterCreatedAt, afterID, limit)
    .all<SendSummary>();
  return results;
}
