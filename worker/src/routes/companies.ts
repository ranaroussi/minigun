import { Hono } from 'hono';
import { Env } from '../env';
import { isValidSlug } from '../lib/slug';
import {
  createCompany,
  getCompanyByID,
  listCompanies,
  listsForCompany,
  resolveCompany,
} from '../store/companies';
import { AlreadyExistsError, NotFoundError } from '../store/types';

export function mountCompanies(app: Hono<{ Bindings: Env }>) {
  app.post('/companies', async (c) => {
    const body = await c
      .req.json<{ name?: string; slug?: string; domain?: string }>()
      .catch(() => null);
    if (!body) return c.json({ error: 'invalid JSON' }, 400);
    const name = (body.name ?? '').trim();
    const slug = (body.slug ?? '').trim().toLowerCase();
    const domain = (body.domain ?? '').trim().toLowerCase();
    if (!name) return c.json({ error: 'name is required' }, 400);
    if (!isValidSlug(slug)) {
      return c.json({ error: 'slug must be lowercase alphanumerics or hyphens, 1-64 chars' }, 400);
    }
    if (!domain) {
      return c.json({ error: 'domain is required (Mailgun sending domain for this company)' }, 400);
    }
    try {
      const created = await createCompany(c.env.DB, slug, name, domain);
      return c.json(created, 201);
    } catch (err) {
      if (err instanceof AlreadyExistsError) {
        return c.json({ error: 'company with that slug already exists' }, 409);
      }
      throw err;
    }
  });

  app.get('/companies', async (c) => {
    const items = await listCompanies(c.env.DB);
    return c.json({ items });
  });

  app.get('/companies/:company', async (c) => {
    const key = c.req.param('company');
    try {
      const company = await resolveCompany(c.env.DB, key);
      return c.json(company);
    } catch (err) {
      if (err instanceof NotFoundError) return c.json({ error: 'company not found' }, 404);
      throw err;
    }
  });

  app.get('/companies/:company/lists', async (c) => {
    const key = c.req.param('company');
    try {
      const company = await resolveCompany(c.env.DB, key);
      const items = await listsForCompany(c.env.DB, company.id);
      return c.json({ company, items });
    } catch (err) {
      if (err instanceof NotFoundError) return c.json({ error: 'company not found' }, 404);
      throw err;
    }
  });
}
