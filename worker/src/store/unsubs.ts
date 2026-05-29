import { newUnsub } from '../lib/ids';
import { incrementSendStatsUnsubscribedStmt } from './stats';
import { NotFoundError, Subscription, nowISO } from './types';

export type UnsubscribeEvent = {
  id: string;
  send_id: string | null;
  subscription_id: number;
  list_id: string;
  contact_id: string;
  email: string;
};

// Audit reason values for the unsubscribe_events.reason column. See the
// 0008_unsub_reason migration for full semantics.
export type UnsubReason =
  | ''
  | 'auto-prune'
  | 'auto-prune-by-count'
  | 'auto-prune-by-recency'
  | 'auto-prune-by-no-delivery'
  | 'manual';

export async function recordUnsubscribeEvent(
  db: D1Database,
  sendID: string | null,
  sub: Subscription,
  email: string,
  reason: UnsubReason = '',
): Promise<UnsubscribeEvent> {
  const id = newUnsub();
  const now = nowISO();
  const reasonVal = reason === '' ? null : reason;
  const insertEvent = db
    .prepare(
      `INSERT INTO unsubscribe_events
        (id, send_id, subscription_id, list_id, contact_id, email, created_at, reason)
       VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
    )
    .bind(id, sendID, sub.id, sub.list_id, sub.contact_id, email, now, reasonVal);
  if (sendID) {
    await db.batch([insertEvent, incrementSendStatsUnsubscribedStmt(db, sendID)]);
  } else {
    await insertEvent.run();
  }
  return {
    id,
    send_id: sendID,
    subscription_id: sub.id,
    list_id: sub.list_id,
    contact_id: sub.contact_id,
    email,
  };
}

// Atomically (in one D1 batch) flip the subscription to unsubscribed AND
// write the unsubscribe_events audit row. The Phase 4 prune executor used
// two separate calls and left a window where the unsubscribe could
// commit but the audit insert could fail, leaving an unsubscribe without
// a reason audit. Phase 5 closes that window.
//
// D1 doesn't support inter-statement transactions like SQLite's BEGIN/
// COMMIT, but db.batch() executes its statements atomically — either all
// commit or none do. We do the read (subscription lookup) outside the
// batch since we need its id for the two writes; this opens a tiny race
// where another writer could unsubscribe in between, which is benign
// (UPDATE becomes a no-op for subscribed=0 → subscribed=0 but the audit
// row still gets written, which is exactly what we want).
//
// Throws NotFoundError when no subscribed row exists. Caller skips
// silently in that case.
export async function unsubscribeAndAudit(
  db: D1Database,
  listID: string,
  contactID: string,
  email: string,
  reason: UnsubReason,
): Promise<{ subscriptionID: number; eventID: string }> {
  const now = nowISO();
  const row = await db
    .prepare(
      `SELECT id, subscribed FROM subscriptions WHERE list_id = ? AND contact_id = ?`,
    )
    .bind(listID, contactID)
    .first<{ id: number; subscribed: number }>();
  if (!row) throw new NotFoundError();
  if (row.subscribed === 0) throw new NotFoundError();
  const auditID = newUnsub();
  const reasonVal = reason === '' ? null : reason;
  await db.batch([
    db
      .prepare(
        'UPDATE subscriptions SET subscribed = 0, unsubscribed_at = ?, updated_at = ? WHERE id = ?',
      )
      .bind(now, now, row.id),
    db
      .prepare(
        `INSERT INTO unsubscribe_events
           (id, send_id, subscription_id, list_id, contact_id, email, created_at, reason)
         VALUES (?, NULL, ?, ?, ?, ?, ?, ?)`,
      )
      .bind(auditID, row.id, listID, contactID, email, now, reasonVal),
  ]);
  return { subscriptionID: row.id, eventID: auditID };
}

export async function countUnsubscribesForSend(db: D1Database, sendID: string): Promise<number> {
  const row = await db
    .prepare('SELECT COUNT(*) AS n FROM unsubscribe_events WHERE send_id = ?')
    .bind(sendID)
    .first<{ n: number }>();
  return row?.n ?? 0;
}
