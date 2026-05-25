import { Hono } from 'hono';
import { Env } from '../env';
import { decodeCursor, clampLimit, encodeCursor } from '../lib/pagination';
import { isValidSlug } from '../lib/slug';
import { resolveCompany } from '../store/companies';
import { listContactsInList } from '../store/contacts';
import { createList, getListDetails, listLists, resolveList } from '../store/lists';
import { AlreadyExistsError, NotFoundError } from '../store/types';

export function mountLists(app: Hono<{ Bindings: Env }>) {
  app.post('/lists', async (c) => {
    const body = await c.req.json<{
      name?: string;
      slug?: string;
      company?: string;
      domain?: string;
      description?: string;
      weight?: number;
    }>().catch(() => null);
    if (!body) return c.json({ error: 'invalid JSON' }, 400);
    const name = (body.name ?? '').trim();
    const slug = (body.slug ?? '').trim().toLowerCase();
    const companyKey = (body.company ?? '').trim();
    const explicitDomain = (body.domain ?? '').trim().toLowerCase();
    if (!name) return c.json({ error: 'name is required' }, 400);
    if (!isValidSlug(slug)) {
      return c.json({ error: 'slug must be lowercase alphanumerics or hyphens, 1-64 chars' }, 400);
    }
    if (!companyKey) return c.json({ error: 'company is required (id or slug)' }, 400);
    let companyID: string;
    let inheritedDomain: string;
    try {
      const company = await resolveCompany(c.env.DB, companyKey);
      companyID = company.id;
      inheritedDomain = company.sending_domain;
    } catch (err) {
      if (err instanceof NotFoundError) return c.json({ error: 'company not found' }, 404);
      throw err;
    }
    const sendingDomain = explicitDomain || inheritedDomain;
    if (!sendingDomain) {
      return c.json(
        { error: 'domain is required and parent company has no sending_domain configured' },
        400,
      );
    }
    const weight = body.weight && body.weight > 0 ? body.weight : 10;
    try {
      const list = await createList(c.env.DB, {
        slug,
        name,
        company_id: companyID,
        sending_domain: sendingDomain,
        description: body.description ?? '',
        weight,
      });
      return c.json(list, 201);
    } catch (err) {
      if (err instanceof AlreadyExistsError) {
        return c.json({ error: 'list with that slug already exists' }, 409);
      }
      throw err;
    }
  });

  app.get('/lists', async (c) => {
    const items = await listLists(c.env.DB);
    return c.json({ items });
  });

  app.get('/lists/:list', async (c) => {
    const key = c.req.param('list');
    try {
      const list = await resolveList(c.env.DB, key);
      const details = await getListDetails(c.env.DB, list.id);
      return c.json(details);
    } catch (err) {
      if (err instanceof NotFoundError) return c.json({ error: 'list not found' }, 404);
      throw err;
    }
  });

  app.get('/lists/:list/contacts', async (c) => {
    const key = c.req.param('list');
    let list;
    try {
      list = await resolveList(c.env.DB, key);
    } catch (err) {
      if (err instanceof NotFoundError) return c.json({ error: 'list not found' }, 404);
      throw err;
    }
    let cursor;
    try {
      cursor = decodeCursor(c.req.query('cursor'));
    } catch {
      return c.json({ error: 'invalid cursor' }, 400);
    }
    const limit = clampLimit(Number(c.req.query('limit') ?? '0'));
    const items = await listContactsInList(
      c.env.DB,
      list.id,
      cursor.afterIntID ?? 0,
      limit + 1,
    );
    const hasMore = items.length > limit;
    const trimmed = hasMore ? items.slice(0, limit) : items;
    const last = trimmed[trimmed.length - 1];
    const nextCursor = hasMore && last ? encodeCursor({ afterIntID: last.subscription_id }) : '';
    return c.json({ items: trimmed, next_cursor: nextCursor, has_more: hasMore });
  });
}
