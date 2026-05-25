import { newCompany } from '../lib/ids';
import {
  AlreadyExistsError,
  Company,
  CompanySummary,
  List,
  NotFoundError,
  isUniqueViolation,
  nowISO,
} from './types';

export async function createCompany(
  db: D1Database,
  slug: string,
  name: string,
  sendingDomain: string,
): Promise<Company> {
  const id = newCompany();
  const now = nowISO();
  try {
    await db
      .prepare(
        'INSERT INTO companies (id, slug, name, sending_domain, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)',
      )
      .bind(id, slug, name, sendingDomain, now, now)
      .run();
  } catch (err) {
    if (isUniqueViolation(err)) throw new AlreadyExistsError();
    throw err;
  }
  return (await getCompanyByID(db, id)) as Company;
}

const COMPANY_SELECT = 'SELECT id, slug, name, sending_domain, created_at, updated_at FROM companies';

export async function getCompanyByID(db: D1Database, id: string): Promise<Company> {
  const row = await db
    .prepare(`${COMPANY_SELECT} WHERE id = ?`)
    .bind(id)
    .first<Company>();
  if (!row) throw new NotFoundError();
  return row;
}

export async function getCompanyBySlug(db: D1Database, slug: string): Promise<Company> {
  const row = await db
    .prepare(`${COMPANY_SELECT} WHERE slug = ?`)
    .bind(slug)
    .first<Company>();
  if (!row) throw new NotFoundError();
  return row;
}

export async function resolveCompany(db: D1Database, idOrSlug: string): Promise<Company> {
  try {
    return await getCompanyByID(db, idOrSlug);
  } catch {
    return await getCompanyBySlug(db, idOrSlug);
  }
}

export async function listCompanies(db: D1Database): Promise<CompanySummary[]> {
  const { results } = await db
    .prepare(
      `SELECT c.id, c.slug, c.name, c.sending_domain, c.created_at, c.updated_at,
              COALESCE(COUNT(l.id), 0) AS list_count
         FROM companies c
         LEFT JOIN lists l ON l.company_id = c.id
         GROUP BY c.id
         ORDER BY c.created_at ASC`,
    )
    .all<CompanySummary>();
  return results;
}

export async function listsForCompany(db: D1Database, companyID: string): Promise<List[]> {
  const { results } = await db
    .prepare(
      `SELECT id, slug, name, COALESCE(description, '') AS description,
              COALESCE(weight, 10) AS weight, COALESCE(company_id, '') AS company_id,
              sending_domain, created_at, updated_at
         FROM lists
        WHERE company_id = ?
        ORDER BY weight ASC, name ASC`,
    )
    .bind(companyID)
    .all<List>();
  return results;
}
