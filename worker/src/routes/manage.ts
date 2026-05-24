import { Hono } from 'hono';
import { Env } from '../env';
import { InvalidTokenError, verify } from '../lib/token';
import { ManagePage } from '../pages/manage';
import { getContactByID } from '../store/contacts';
import { getCompanyByID, listsForCompany } from '../store/companies';
import { getListByID } from '../store/lists';
import {
  applySubscriptionChanges,
  getCompanyListsWithSubscription,
} from '../store/manage';
import { getSend } from '../store/sends';
import { getSubscription, getSubscriptionByID } from '../store/subscriptions';
import {
  NotFoundError,
  SubscriptionChange,
  SubscriptionDelta,
} from '../store/types';
import { recordUnsubscribeEvent } from '../store/unsubs';

function htmlResponse(html: any, status = 200) {
  return new Response(String(html), {
    status,
    headers: { 'content-type': 'text/html; charset=utf-8' },
  });
}

type ManageCtx = {
  token: string;
  send: Awaited<ReturnType<typeof getSend>>;
  contact: Awaited<ReturnType<typeof getContactByID>>;
  list: Awaited<ReturnType<typeof getListByID>>;
  company: Awaited<ReturnType<typeof getCompanyByID>>;
};

async function loadContext(env: Env, tokenStr: string): Promise<ManageCtx> {
  let t;
  try {
    t = await verify(env.MINIGUN_HMAC_SECRET, tokenStr);
  } catch (err) {
    if (err instanceof InvalidTokenError) throw new Error('Invalid or expired manage link.');
    throw err;
  }
  const snd = await getSend(env.DB, t.sendID).catch(() => {
    throw new Error('Send not found.');
  });
  const sub = await getSubscriptionByID(env.DB, t.subscriptionID).catch(() => {
    throw new Error('Subscription not found.');
  });
  const contact = await getContactByID(env.DB, sub.contact_id).catch(() => {
    throw new Error('Contact not found.');
  });
  const list = await getListByID(env.DB, sub.list_id).catch(() => {
    throw new Error('List not found.');
  });
  if (!list.company_id) {
    throw new Error('This list is not associated with a company; manage page is not available.');
  }
  const company = await getCompanyByID(env.DB, list.company_id).catch(() => {
    throw new Error('Company not found.');
  });
  return { token: tokenStr, send: snd, contact, list, company };
}

export function mountManage(app: Hono<{ Bindings: Env }>) {
  app.get('/manage/:token', async (c) => {
    const tokenStr = c.req.param('token');
    try {
      const ctx = await loadContext(c.env, tokenStr);
      const lists = await getCompanyListsWithSubscription(c.env.DB, ctx.company.id, ctx.contact.id);
      return htmlResponse(
        ManagePage({
          email: ctx.contact.email,
          companyName: ctx.company.name,
          token: tokenStr,
          lists,
        }),
      );
    } catch (err) {
      return htmlResponse(
        ManagePage({
          email: '',
          companyName: '',
          token: tokenStr,
          lists: [],
          error: (err as Error).message,
        }),
        400,
      );
    }
  });

  app.post('/manage/:token', async (c) => {
    const tokenStr = c.req.param('token');
    let ctx: ManageCtx;
    try {
      ctx = await loadContext(c.env, tokenStr);
    } catch (err) {
      return htmlResponse(
        ManagePage({
          email: '',
          companyName: '',
          token: tokenStr,
          lists: [],
          error: (err as Error).message,
        }),
        400,
      );
    }

    const form = (await c.req.parseBody({ all: true }).catch(() => ({}))) as Record<
      string,
      string | File | (string | File)[]
    >;
    const raw = form['list'];
    const checked = new Set<string>(
      Array.isArray(raw) ? raw.map(String) : typeof raw === 'string' ? [raw] : [],
    );

    let companyLists;
    try {
      companyLists = await listsForCompany(c.env.DB, ctx.company.id);
    } catch {
      return htmlResponse(
        ManagePage({
          email: ctx.contact.email,
          companyName: ctx.company.name,
          token: tokenStr,
          lists: [],
          error: 'Failed to load preferences.',
        }),
        500,
      );
    }
    const desired: SubscriptionChange[] = companyLists.map((l) => ({
      list_id: l.id,
      subscribed: checked.has(l.id),
    }));

    let deltas: SubscriptionDelta[];
    try {
      deltas = await applySubscriptionChanges(c.env.DB, ctx.contact.id, desired);
    } catch (err) {
      console.error('apply changes', err);
      return htmlResponse(
        ManagePage({
          email: ctx.contact.email,
          companyName: ctx.company.name,
          token: tokenStr,
          lists: [],
          error: 'Failed to save preferences.',
        }),
        500,
      );
    }

    for (const d of deltas) {
      if (d.was_subbed && !d.now_subbed) {
        try {
          const sub = await getSubscription(c.env.DB, d.list_id, ctx.contact.id);
          await recordUnsubscribeEvent(c.env.DB, ctx.send.id, sub, ctx.contact.email);
        } catch (err) {
          console.error('record unsub event from /manage', err);
        }
      }
    }

    return htmlResponse(
      ManagePage({
        email: ctx.contact.email,
        companyName: ctx.company.name,
        token: tokenStr,
        lists: [],
        done: true,
        deltas,
      }),
    );
  });
}
