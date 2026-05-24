import { getContactByEmail, normalizeEmail } from './contacts';
import { NotFoundError, Recipient, Subscription, nowISO } from './types';

type SubscriptionRow = Omit<Subscription, 'subscribed'> & { subscribed: number };

function rowToSub(r: SubscriptionRow): Subscription {
  return { ...r, subscribed: r.subscribed === 1 };
}

export async function upsertSubscription(
  db: D1Database,
  listID: string,
  contactID: string,
): Promise<Subscription> {
  const now = nowISO();
  const existing = await db
    .prepare(
      `SELECT id, list_id, contact_id, subscribed, subscribed_at, updated_at, unsubscribed_at
         FROM subscriptions WHERE list_id = ? AND contact_id = ?`,
    )
    .bind(listID, contactID)
    .first<SubscriptionRow>();

  if (!existing) {
    const res = await db
      .prepare(
        `INSERT INTO subscriptions (list_id, contact_id, subscribed, subscribed_at, updated_at)
         VALUES (?, ?, 1, ?, ?)`,
      )
      .bind(listID, contactID, now, now)
      .run();
    const id = res.meta.last_row_id!;
    return getSubscriptionByID(db, id);
  }

  if (existing.subscribed !== 1) {
    await db
      .prepare(
        `UPDATE subscriptions SET subscribed = 1, subscribed_at = ?, unsubscribed_at = NULL, updated_at = ?
         WHERE id = ?`,
      )
      .bind(now, now, existing.id)
      .run();
  } else {
    await db
      .prepare('UPDATE subscriptions SET updated_at = ? WHERE id = ?')
      .bind(now, existing.id)
      .run();
  }
  return getSubscriptionByID(db, existing.id);
}

export async function unsubscribeByListAndEmail(
  db: D1Database,
  listID: string,
  email: string,
): Promise<Subscription> {
  const contact = await getContactByEmail(db, normalizeEmail(email));
  return unsubscribeSubscription(db, listID, contact.id);
}

export async function unsubscribeSubscription(
  db: D1Database,
  listID: string,
  contactID: string,
): Promise<Subscription> {
  const now = nowISO();
  const existing = await db
    .prepare(
      `SELECT id, list_id, contact_id, subscribed, subscribed_at, updated_at, unsubscribed_at
         FROM subscriptions WHERE list_id = ? AND contact_id = ?`,
    )
    .bind(listID, contactID)
    .first<SubscriptionRow>();
  if (!existing) throw new NotFoundError();
  await db
    .prepare(
      'UPDATE subscriptions SET subscribed = 0, unsubscribed_at = ?, updated_at = ? WHERE id = ?',
    )
    .bind(now, now, existing.id)
    .run();
  return getSubscriptionByID(db, existing.id);
}

export async function getSubscriptionByID(db: D1Database, id: number): Promise<Subscription> {
  const row = await db
    .prepare(
      `SELECT id, list_id, contact_id, subscribed, subscribed_at, updated_at, unsubscribed_at
         FROM subscriptions WHERE id = ?`,
    )
    .bind(id)
    .first<SubscriptionRow>();
  if (!row) throw new NotFoundError();
  return rowToSub(row);
}

export async function getSubscription(
  db: D1Database,
  listID: string,
  contactID: string,
): Promise<Subscription> {
  const row = await db
    .prepare(
      `SELECT id, list_id, contact_id, subscribed, subscribed_at, updated_at, unsubscribed_at
         FROM subscriptions WHERE list_id = ? AND contact_id = ?`,
    )
    .bind(listID, contactID)
    .first<SubscriptionRow>();
  if (!row) throw new NotFoundError();
  return rowToSub(row);
}

export async function maxSubscriptionID(db: D1Database, listID: string): Promise<number> {
  const row = await db
    .prepare('SELECT COALESCE(MAX(id), 0) AS n FROM subscriptions WHERE list_id = ? AND subscribed = 1')
    .bind(listID)
    .first<{ n: number }>();
  return row?.n ?? 0;
}

export async function countSubscribed(
  db: D1Database,
  listID: string,
  maxID: number,
): Promise<number> {
  const row = await db
    .prepare(
      'SELECT COUNT(*) AS n FROM subscriptions WHERE list_id = ? AND subscribed = 1 AND id <= ?',
    )
    .bind(listID, maxID)
    .first<{ n: number }>();
  return row?.n ?? 0;
}

export async function nextRecipientBatch(
  db: D1Database,
  listID: string,
  afterID: number,
  maxID: number,
  limit: number,
): Promise<Recipient[]> {
  const { results } = await db
    .prepare(
      `SELECT subs.id AS subscription_id, c.id AS contact_id, c.email, c.params
         FROM subscriptions subs
         JOIN contacts c ON c.id = subs.contact_id
        WHERE subs.list_id = ?
          AND subs.subscribed = 1
          AND subs.id > ?
          AND subs.id <= ?
        ORDER BY subs.id ASC
        LIMIT ?`,
    )
    .bind(listID, afterID, maxID, limit)
    .all<Recipient>();
  return results;
}
