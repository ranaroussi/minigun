import { newUnsub } from '../lib/ids';
import { Subscription, nowISO } from './types';

export type UnsubscribeEvent = {
  id: string;
  send_id: string | null;
  subscription_id: number;
  list_id: string;
  contact_id: string;
  email: string;
};

export async function recordUnsubscribeEvent(
  db: D1Database,
  sendID: string | null,
  sub: Subscription,
  email: string,
): Promise<UnsubscribeEvent> {
  const id = newUnsub();
  const now = nowISO();
  await db
    .prepare(
      `INSERT INTO unsubscribe_events
        (id, send_id, subscription_id, list_id, contact_id, email, created_at)
       VALUES (?, ?, ?, ?, ?, ?, ?)`,
    )
    .bind(id, sendID, sub.id, sub.list_id, sub.contact_id, email, now)
    .run();
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
