import { Hono } from 'hono';
import { Env } from '../env';
import { InvalidTokenError, verify } from '../lib/token';
import { UnsubscribePage } from '../pages/unsubscribe';
import { getContactByID } from '../store/contacts';
import { getListByID } from '../store/lists';
import { getSend } from '../store/sends';
import {
  getSubscriptionByID,
  unsubscribeSubscription,
} from '../store/subscriptions';
import { NotFoundError } from '../store/types';
import { recordUnsubscribeEvent } from '../store/unsubs';

function htmlResponse(html: any, status = 200) {
  return new Response(String(html), {
    status,
    headers: { 'content-type': 'text/html; charset=utf-8' },
  });
}

async function verifyTurnstile(env: Env, captchaToken: string, ip: string): Promise<boolean> {
  if (!env.MINIGUN_TURNSTILE_SECRET_KEY) return true;
  const form = new FormData();
  form.append('secret', env.MINIGUN_TURNSTILE_SECRET_KEY);
  form.append('response', captchaToken);
  if (ip) form.append('remoteip', ip);
  const resp = await fetch('https://challenges.cloudflare.com/turnstile/v0/siteverify', {
    method: 'POST',
    body: form,
  });
  if (!resp.ok) return false;
  const data = (await resp.json()) as { success?: boolean };
  return data.success === true;
}

function clientIP(headers: Headers): string {
  const xff = headers.get('x-forwarded-for');
  if (xff) return xff.split(',')[0]!.trim();
  return headers.get('cf-connecting-ip') ?? headers.get('x-real-ip') ?? '';
}

export function mountUnsubscribe(app: Hono<{ Bindings: Env }>) {
  app.get('/u/:token', async (c) => {
    const tokenStr = c.req.param('token');
    try {
      const t = await verify(c.env.MINIGUN_HMAC_SECRET, tokenStr);
      const snd = await getSend(c.env.DB, t.sendID);
      const sub = await getSubscriptionByID(c.env.DB, t.subscriptionID);
      const contact = await getContactByID(c.env.DB, sub.contact_id);
      let listName = '';
      if (snd.list_id) {
        try {
          listName = (await getListByID(c.env.DB, snd.list_id)).name;
        } catch {}
      }
      if (!sub.subscribed) {
        return htmlResponse(
          UnsubscribePage({ email: contact.email, listName, token: tokenStr, done: true }),
        );
      }
      return htmlResponse(
        UnsubscribePage({
          email: contact.email,
          listName,
          token: tokenStr,
          turnstileSiteKey: c.env.MINIGUN_TURNSTILE_SITE_KEY,
        }),
      );
    } catch (err) {
      const msg =
        err instanceof InvalidTokenError
          ? 'Invalid or expired unsubscribe link.'
          : err instanceof NotFoundError
            ? 'Subscription not found.'
            : 'Invalid or expired unsubscribe link.';
      return htmlResponse(
        UnsubscribePage({ email: '', listName: '', token: tokenStr, error: msg }),
        400,
      );
    }
  });

  app.post('/u/:token', async (c) => {
    const tokenStr = c.req.param('token');
    const form = (await c.req.parseBody().catch(() => ({}))) as Record<string, string | File>;
    const oneClick =
      typeof form['List-Unsubscribe'] === 'string' && form['List-Unsubscribe'] === 'One-Click';

    let t;
    try {
      t = await verify(c.env.MINIGUN_HMAC_SECRET, tokenStr);
    } catch {
      if (oneClick) return new Response(null, { status: 400 });
      return htmlResponse(
        UnsubscribePage({
          email: '',
          listName: '',
          token: tokenStr,
          error: 'Invalid or expired unsubscribe link.',
        }),
        400,
      );
    }

    if (!oneClick && c.env.MINIGUN_TURNSTILE_SITE_KEY) {
      const captcha = typeof form['cf-turnstile-response'] === 'string' ? form['cf-turnstile-response'] : '';
      const ok = await verifyTurnstile(c.env, captcha, clientIP(c.req.raw.headers));
      if (!ok) {
        return htmlResponse(
          UnsubscribePage({
            email: '',
            listName: '',
            token: tokenStr,
            error: 'Bot challenge failed. Please try again.',
            turnstileSiteKey: c.env.MINIGUN_TURNSTILE_SITE_KEY,
          }),
          400,
        );
      }
    }

    let snd;
    try {
      snd = await getSend(c.env.DB, t.sendID);
    } catch {
      if (oneClick) return new Response(null, { status: 200 });
      return htmlResponse(
        UnsubscribePage({
          email: '',
          listName: '',
          token: tokenStr,
          error: 'Send not found.',
        }),
        400,
      );
    }

    let sub;
    try {
      sub = await getSubscriptionByID(c.env.DB, t.subscriptionID);
    } catch {
      if (oneClick) return new Response(null, { status: 200 });
      return htmlResponse(
        UnsubscribePage({
          email: '',
          listName: '',
          token: tokenStr,
          error: 'Already unsubscribed.',
        }),
        400,
      );
    }
    if (sub.subscribed) {
      sub = await unsubscribeSubscription(c.env.DB, sub.list_id, sub.contact_id);
    }
    let email = '';
    try {
      email = (await getContactByID(c.env.DB, sub.contact_id)).email;
    } catch {}
    if (email) {
      await recordUnsubscribeEvent(c.env.DB, snd.id, sub, email).catch((err) =>
        console.error('record unsub event', err),
      );
    }

    if (oneClick) return new Response(null, { status: 200 });

    if (snd.unsubscribe_mode === 'external' && snd.unsubscribe_external_url) {
      return Response.redirect(withUnsubQuery(snd.unsubscribe_external_url, sub.id, snd.list_id), 302);
    }
    if ((snd.unsubscribe_mode as string) === 'redirect' && snd.unsubscribe_redirect_url) {
      return Response.redirect(withUnsubQuery(snd.unsubscribe_redirect_url, sub.id, snd.list_id), 302);
    }

    let listName = '';
    if (snd.list_id) {
      try {
        listName = (await getListByID(c.env.DB, snd.list_id)).name;
      } catch {}
    }
    return htmlResponse(UnsubscribePage({ email, listName, token: tokenStr, done: true }));
  });
}

function withUnsubQuery(base: string, subID: number, listID?: string | null): string {
  try {
    const u = new URL(base);
    if (listID) u.searchParams.set('list', listID);
    u.searchParams.set('subscription_id', String(subID));
    return u.toString();
  } catch {
    return base;
  }
}
