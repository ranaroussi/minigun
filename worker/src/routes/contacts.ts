import { Hono } from 'hono';
import { Env } from '../env';
import { resolveList } from '../store/lists';
import { upsertContact } from '../store/contacts';
import {
  unsubscribeByListAndEmail,
  upsertSubscription,
} from '../store/subscriptions';
import { NotFoundError } from '../store/types';
import { recordUnsubscribeEvent } from '../store/unsubs';

export function mountContacts(app: Hono<{ Bindings: Env }>) {
  app.post('/lists/:list/contacts', async (c) => {
    const key = c.req.param('list');
    let list;
    try {
      list = await resolveList(c.env.DB, key);
    } catch (err) {
      if (err instanceof NotFoundError) return c.json({ error: 'list not found' }, 404);
      throw err;
    }
    const body = await c.req.json<{ email?: string; params?: Record<string, unknown> }>().catch(() => null);
    if (!body) return c.json({ error: 'invalid JSON' }, 400);
    const email = (body.email ?? '').trim();
    if (!email) return c.json({ error: 'email is required' }, 400);
    const contact = await upsertContact(c.env.DB, email, body.params);
    const sub = await upsertSubscription(c.env.DB, list.id, contact.id);
    return c.json({
      contact,
      subscription: {
        id: sub.id,
        list_id: sub.list_id,
        contact_id: sub.contact_id,
        subscribed: sub.subscribed,
      },
    });
  });

  app.post('/lists/:list/unsubscribe', async (c) => {
    const key = c.req.param('list');
    let list;
    try {
      list = await resolveList(c.env.DB, key);
    } catch (err) {
      if (err instanceof NotFoundError) return c.json({ error: 'list not found' }, 404);
      throw err;
    }
    const body = await c.req.json<{ email?: string }>().catch(() => null);
    if (!body) return c.json({ error: 'invalid JSON' }, 400);
    const email = (body.email ?? '').trim();
    if (!email) return c.json({ error: 'email is required' }, 400);
    let sub;
    try {
      sub = await unsubscribeByListAndEmail(c.env.DB, list.id, email);
    } catch (err) {
      if (err instanceof NotFoundError) {
        return c.json({ error: 'no subscription found for that email' }, 404);
      }
      throw err;
    }
    try {
      await recordUnsubscribeEvent(c.env.DB, null, sub, email);
    } catch (err) {
      console.error('record unsub event', err);
    }
    return c.json({ ok: true, subscription_id: sub.id });
  });
}
