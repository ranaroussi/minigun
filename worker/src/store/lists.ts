import { newList } from '../lib/ids';
import {
  AlreadyExistsError,
  List,
  ListDetails,
  ListSummary,
  NotFoundError,
  isUniqueViolation,
  nowISO,
} from './types';

export type NewListParams = {
  slug: string;
  name: string;
  company_id: string;
  sending_domain: string;
  description?: string;
  weight?: number;
};

const LIST_SELECT = `SELECT id, slug, name, COALESCE(description, '') AS description,
       COALESCE(weight, 10) AS weight, COALESCE(company_id, '') AS company_id,
       sending_domain, created_at, updated_at FROM lists`;

export async function createList(db: D1Database, p: NewListParams): Promise<List> {
  const id = newList();
  const now = nowISO();
  const weight = p.weight ?? 10;
  const description = p.description ?? '';
  try {
    await db
      .prepare(
        `INSERT INTO lists (id, slug, name, description, weight, company_id, sending_domain, created_at, updated_at)
         VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
      )
      .bind(id, p.slug, p.name, description, weight, p.company_id, p.sending_domain, now, now)
      .run();
  } catch (err) {
    if (isUniqueViolation(err)) throw new AlreadyExistsError();
    throw err;
  }
  return getListByID(db, id);
}

export async function getListByID(db: D1Database, id: string): Promise<List> {
  const row = await db.prepare(`${LIST_SELECT} WHERE id = ?`).bind(id).first<List>();
  if (!row) throw new NotFoundError();
  return row;
}

export async function getListBySlug(db: D1Database, slug: string): Promise<List> {
  const row = await db.prepare(`${LIST_SELECT} WHERE slug = ?`).bind(slug).first<List>();
  if (!row) throw new NotFoundError();
  return row;
}

export async function resolveList(db: D1Database, idOrSlug: string): Promise<List> {
  try {
    return await getListByID(db, idOrSlug);
  } catch {
    return await getListBySlug(db, idOrSlug);
  }
}

export async function listLists(db: D1Database): Promise<ListSummary[]> {
  const { results } = await db
    .prepare(
      `SELECT l.id, l.slug, l.name, COALESCE(l.description, '') AS description,
              COALESCE(l.weight, 10) AS weight, COALESCE(l.company_id, '') AS company_id,
              l.sending_domain, l.created_at, l.updated_at,
              COALESCE(SUM(CASE WHEN subs.subscribed = 1 THEN 1 ELSE 0 END), 0) AS subscribed_count
         FROM lists l
         LEFT JOIN subscriptions subs ON subs.list_id = l.id
         GROUP BY l.id
         ORDER BY l.weight ASC, l.name ASC`,
    )
    .all<ListSummary>();
  return results;
}

export async function getListDetails(db: D1Database, listID: string): Promise<ListDetails> {
  const row = await db
    .prepare(
      `SELECT l.id, l.slug, l.name, COALESCE(l.description, '') AS description,
              COALESCE(l.weight, 10) AS weight, COALESCE(l.company_id, '') AS company_id,
              l.sending_domain, l.created_at, l.updated_at,
              COALESCE(SUM(CASE WHEN subs.subscribed = 1 THEN 1 ELSE 0 END), 0) AS subscribed_count,
              COUNT(subs.id) AS total_count
         FROM lists l
         LEFT JOIN subscriptions subs ON subs.list_id = l.id
        WHERE l.id = ?
        GROUP BY l.id`,
    )
    .bind(listID)
    .first<ListSummary & { total_count: number }>();
  if (!row || !row.id) throw new NotFoundError();
  const lastSend = await db
    .prepare('SELECT MAX(created_at) AS last_send_at FROM sends WHERE list_id = ?')
    .bind(listID)
    .first<{ last_send_at: string | null }>();
  return { ...row, last_send_at: lastSend?.last_send_at ?? null };
}
