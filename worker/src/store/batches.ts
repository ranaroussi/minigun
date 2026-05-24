import { newBatch } from '../lib/ids';
import { BatchStatus, NotFoundError, SendBatch, nowISO } from './types';

export async function createBatch(
  db: D1Database,
  sendID: string,
  batchIndex: number,
  startID: number,
  endID: number,
  recipientCount: number,
): Promise<SendBatch> {
  const id = newBatch();
  const now = nowISO();
  await db
    .prepare(
      `INSERT INTO send_batches
        (id, send_id, batch_index, start_subscription_id, end_subscription_id,
         recipient_count, status, created_at, updated_at)
       VALUES (?, ?, ?, ?, ?, ?, 'in_flight', ?, ?)`,
    )
    .bind(id, sendID, batchIndex, startID, endID, recipientCount, now, now)
    .run();
  return getBatch(db, id);
}

export async function getBatch(db: D1Database, id: string): Promise<SendBatch> {
  const row = await db
    .prepare(
      `SELECT id, send_id, batch_index, start_subscription_id, end_subscription_id,
              recipient_count, status, mailgun_response, created_at, updated_at
         FROM send_batches WHERE id = ?`,
    )
    .bind(id)
    .first<SendBatch>();
  if (!row) throw new NotFoundError();
  return row;
}

export async function markBatchStatus(
  db: D1Database,
  id: string,
  status: BatchStatus,
  response: string | null,
): Promise<void> {
  const now = nowISO();
  await db
    .prepare('UPDATE send_batches SET status = ?, mailgun_response = ?, updated_at = ? WHERE id = ?')
    .bind(status, response, now, id)
    .run();
}

export async function nextBatchIndex(db: D1Database, sendID: string): Promise<number> {
  const row = await db
    .prepare(
      'SELECT COALESCE(MAX(batch_index), -1) + 1 AS n FROM send_batches WHERE send_id = ?',
    )
    .bind(sendID)
    .first<{ n: number }>();
  return row?.n ?? 0;
}

export async function sendProgress(
  db: D1Database,
  sendID: string,
): Promise<{ completed: number; sent: number }> {
  const row = await db
    .prepare(
      `SELECT COUNT(*) AS completed, COALESCE(SUM(recipient_count), 0) AS sent
         FROM send_batches WHERE send_id = ? AND status = 'succeeded'`,
    )
    .bind(sendID)
    .first<{ completed: number; sent: number }>();
  return { completed: row?.completed ?? 0, sent: row?.sent ?? 0 };
}
