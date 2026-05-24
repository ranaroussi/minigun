import {
  List,
  ManageListState,
  SubscriptionChange,
  SubscriptionDelta,
  nowISO,
} from './types';

export async function getCompanyListsWithSubscription(
  db: D1Database,
  companyID: string,
  contactID: string,
): Promise<ManageListState[]> {
  const { results } = await db
    .prepare(
      `SELECT l.id, l.slug, l.name, COALESCE(l.description, '') AS description,
              COALESCE(l.weight, 10) AS weight, COALESCE(l.company_id, '') AS company_id,
              l.created_at, l.updated_at,
              subs.subscribed AS subscribed, subs.subscribed_at AS subscribed_at
         FROM lists l
         LEFT JOIN subscriptions subs ON subs.list_id = l.id AND subs.contact_id = ?
        WHERE l.company_id = ?
        ORDER BY l.weight ASC, l.name ASC`,
    )
    .bind(contactID, companyID)
    .all<List & { subscribed: number | null; subscribed_at: string | null }>();
  return results.map((r) => {
    const list: List = {
      id: r.id,
      slug: r.slug,
      name: r.name,
      description: r.description,
      weight: r.weight,
      company_id: r.company_id,
      created_at: r.created_at,
      updated_at: r.updated_at,
    };
    return {
      list,
      subscribed: r.subscribed === 1,
      subscribed_at: r.subscribed_at,
    };
  });
}

export async function applySubscriptionChanges(
  db: D1Database,
  contactID: string,
  desired: SubscriptionChange[],
): Promise<SubscriptionDelta[]> {
  if (desired.length === 0) return [];
  const now = nowISO();
  const listIDs = desired.map((d) => d.list_id);
  const placeholders = listIDs.map(() => '?').join(', ');

  const namesRes = await db
    .prepare(`SELECT id, name FROM lists WHERE id IN (${placeholders})`)
    .bind(...listIDs)
    .all<{ id: string; name: string }>();
  const nameByID = new Map(namesRes.results.map((r) => [r.id, r.name]));

  const subsRes = await db
    .prepare(
      `SELECT id, list_id, subscribed FROM subscriptions
        WHERE contact_id = ? AND list_id IN (${placeholders})`,
    )
    .bind(contactID, ...listIDs)
    .all<{ id: number; list_id: string; subscribed: number }>();
  const subByList = new Map(subsRes.results.map((r) => [r.list_id, r]));

  const ops: D1PreparedStatement[] = [];
  const deltas: SubscriptionDelta[] = [];
  for (const ch of desired) {
    const listName = nameByID.get(ch.list_id);
    if (!listName) continue;
    const existing = subByList.get(ch.list_id);
    const wasSubbed = existing ? existing.subscribed === 1 : false;
    if (existing && wasSubbed === ch.subscribed) continue;

    if (!existing) {
      if (!ch.subscribed) continue;
      ops.push(
        db
          .prepare(
            `INSERT INTO subscriptions (list_id, contact_id, subscribed, subscribed_at, updated_at)
             VALUES (?, ?, 1, ?, ?)`,
          )
          .bind(ch.list_id, contactID, now, now),
      );
    } else if (ch.subscribed) {
      ops.push(
        db
          .prepare(
            `UPDATE subscriptions SET subscribed = 1, subscribed_at = ?, unsubscribed_at = NULL,
                                       updated_at = ? WHERE id = ?`,
          )
          .bind(now, now, existing.id),
      );
    } else {
      ops.push(
        db
          .prepare(
            `UPDATE subscriptions SET subscribed = 0, unsubscribed_at = ?, updated_at = ?
              WHERE id = ?`,
          )
          .bind(now, now, existing.id),
      );
    }
    deltas.push({
      list_id: ch.list_id,
      list_name: listName,
      was_subbed: wasSubbed,
      now_subbed: ch.subscribed,
    });
  }

  if (ops.length > 0) await db.batch(ops);
  return deltas;
}
