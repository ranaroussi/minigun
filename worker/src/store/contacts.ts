import { newContact } from '../lib/ids';
import { Contact, ListContactRow, NotFoundError, nowISO } from './types';

export function normalizeEmail(e: string): string {
  return e.trim().toLowerCase();
}

export async function upsertContact(
  db: D1Database,
  email: string,
  params: Record<string, unknown> | undefined,
): Promise<Contact> {
  const normalized = normalizeEmail(email);
  if (!normalized) throw new Error('email is required');

  const existing = await db
    .prepare('SELECT id, email, params, created_at, updated_at FROM contacts WHERE email = ?')
    .bind(normalized)
    .first<Contact>();

  const now = nowISO();
  if (!existing) {
    const id = newContact();
    const paramsJSON = params ? JSON.stringify(params) : '{}';
    await db
      .prepare('INSERT INTO contacts (id, email, params, created_at, updated_at) VALUES (?, ?, ?, ?, ?)')
      .bind(id, normalized, paramsJSON, now, now)
      .run();
    return getContactByID(db, id);
  }

  let merged: Record<string, unknown> = {};
  try {
    merged = existing.params ? (JSON.parse(existing.params) as Record<string, unknown>) : {};
  } catch {
    merged = {};
  }
  if (params) for (const [k, v] of Object.entries(params)) merged[k] = v;
  await db
    .prepare('UPDATE contacts SET params = ?, updated_at = ? WHERE id = ?')
    .bind(JSON.stringify(merged), now, existing.id)
    .run();
  return getContactByID(db, existing.id);
}

export async function getContactByID(db: D1Database, id: string): Promise<Contact> {
  const row = await db
    .prepare('SELECT id, email, params, created_at, updated_at FROM contacts WHERE id = ?')
    .bind(id)
    .first<Contact>();
  if (!row) throw new NotFoundError();
  return row;
}

export type ContactDeletionResult = {
  contact: Contact;
  subscriptions_removed: number;
  unsub_events_removed: number;
};

// Permanently removes a contact and every row that references them
// (subscriptions + the historical unsubscribe_events audit log).
// Intended for hard-bounce cleanup; do NOT use as the normal opt-out
// path (that should remain UpsertSubscription({subscribed: 0}) so the
// suppression record survives).
//
// Atomicity: D1's batch() commits all statements in a single
// transaction, so a mid-delete failure leaves the contact intact.
export async function deleteContact(
  db: D1Database,
  idOrEmail: string,
): Promise<ContactDeletionResult> {
  const key = idOrEmail.trim();
  if (!key) throw new Error('id or email is required');
  const contact = key.startsWith('c_')
    ? await getContactByID(db, key)
    : await getContactByEmail(db, key);

  const results = await db.batch([
    db.prepare('DELETE FROM unsubscribe_events WHERE contact_id = ?').bind(contact.id),
    db.prepare('DELETE FROM subscriptions WHERE contact_id = ?').bind(contact.id),
    db.prepare('DELETE FROM contacts WHERE id = ?').bind(contact.id),
  ]);
  return {
    contact,
    unsub_events_removed: results[0]?.meta?.changes ?? 0,
    subscriptions_removed: results[1]?.meta?.changes ?? 0,
  };
}

export async function getContactByEmail(db: D1Database, email: string): Promise<Contact> {
  const row = await db
    .prepare('SELECT id, email, params, created_at, updated_at FROM contacts WHERE email = ?')
    .bind(normalizeEmail(email))
    .first<Contact>();
  if (!row) throw new NotFoundError();
  return row;
}

export async function listContactsInList(
  db: D1Database,
  listID: string,
  afterSubID: number,
  limit: number,
): Promise<ListContactRow[]> {
  type Row = {
    subscription_id: number;
    contact_id: string;
    email: string;
    params: string;
    subscribed: number;
    subscribed_at: string | null;
    unsubscribed_at: string | null;
  };
  const { results } = await db
    .prepare(
      `SELECT subs.id AS subscription_id, c.id AS contact_id, c.email, c.params,
              subs.subscribed AS subscribed, subs.subscribed_at, subs.unsubscribed_at
         FROM subscriptions subs
         JOIN contacts c ON c.id = subs.contact_id
        WHERE subs.list_id = ? AND subs.id > ?
        ORDER BY subs.id ASC
        LIMIT ?`,
    )
    .bind(listID, afterSubID, limit)
    .all<Row>();
  return results.map((r) => ({
    subscription_id: r.subscription_id,
    contact_id: r.contact_id,
    email: r.email,
    params: r.params,
    subscribed: r.subscribed === 1,
    subscribed_at: r.subscribed_at,
    unsubscribed_at: r.unsubscribed_at,
  }));
}
