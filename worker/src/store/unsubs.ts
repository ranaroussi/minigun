import { newUnsub } from '../lib/ids';
import { incrementSendStatsUnsubscribedStmt } from './stats';
import { Subscription, nowISO } from './types';

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

export async function countUnsubscribesForSend(db: D1Database, sendID: string): Promise<number> {
  const row = await db
    .prepare('SELECT COUNT(*) AS n FROM unsubscribe_events WHERE send_id = ?')
    .bind(sendID)
    .first<{ n: number }>();
  return row?.n ?? 0;
}
