import { Hono } from 'hono';
import { Env } from '../env';
import {
  listContactEngagement,
  listSendEvents,
  resolveContactID,
} from '../store/events';
import { resolveList } from '../store/lists';
import { NotFoundError } from '../store/types';

// Opaque keyset cursor — shape matches the Go side so a client can roll
// between Worker and Go-server deployments without re-encoding. `t` is
// event_timestamp_ms (number), `i` is the row id (string).
type EventsCursor = { t: number; i: string };

function encodeCursor(c: EventsCursor): string {
  const json = JSON.stringify(c);
  // base64url, no padding
  const b64 = btoa(json).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
  return b64;
}

function decodeCursor(s: string | null | undefined): EventsCursor | null {
  if (!s) return null;
  let b64 = s.replace(/-/g, '+').replace(/_/g, '/');
  while (b64.length % 4) b64 += '=';
  let raw: string;
  try {
    raw = atob(b64);
  } catch {
    throw new Error('invalid cursor');
  }
  let obj: any;
  try {
    obj = JSON.parse(raw);
  } catch {
    throw new Error('invalid cursor');
  }
  if (typeof obj?.t !== 'number' || typeof obj?.i !== 'string') {
    throw new Error('invalid cursor');
  }
  return { t: obj.t, i: obj.i };
}

export function mountEvents(app: Hono<{ Bindings: Env }>) {
  // GET /sends/{id}/events?event=&since=&limit=&cursor=
  app.get('/sends/:id/events', async (c) => {
    const sendID = c.req.param('id');
    if (!sendID) return c.json({ error: 'send id required' }, 400);

    const eventType = (c.req.query('event') ?? '').trim();
    const sinceRaw = c.req.query('since');
    const limitRaw = c.req.query('limit');
    const cursorRaw = c.req.query('cursor');

    let limit = limitRaw ? parseInt(limitRaw, 10) : 100;
    if (!Number.isFinite(limit) || limit <= 0) limit = 100;
    if (limit > 500) limit = 500;

    let sinceMs = 0;
    if (sinceRaw) {
      const n = parseInt(sinceRaw, 10);
      if (Number.isFinite(n)) sinceMs = n;
    }

    let cursor: EventsCursor | null;
    try {
      cursor = decodeCursor(cursorRaw);
    } catch (err) {
      return c.json({ error: (err as Error).message }, 400);
    }

    const items = await listSendEvents(c.env.DB, {
      sendID,
      eventType: eventType || undefined,
      sinceMs,
      afterTsMs: cursor?.t,
      afterID: cursor?.i,
      limit,
    });

    const resp: Record<string, unknown> = { items };
    // Emit next_cursor only when this page filled to the limit (the
    // next page might be empty but that's a one-extra-request truth).
    if (items.length === limit) {
      const last = items[items.length - 1]!;
      resp.next_cursor = encodeCursor({ t: last.event_timestamp_ms, i: last.id });
    }
    return c.json(resp);
  });

  // GET /contacts/{idOrEmail}/engagement?list_id=
  app.get('/contacts/:idOrEmail/engagement', async (c) => {
    const key = c.req.param('idOrEmail');
    if (!key) return c.json({ error: 'id or email required' }, 400);

    const contactID = await resolveContactID(c.env.DB, decodeURIComponent(key));
    if (!contactID) return c.json({ error: 'contact not found' }, 404);

    let listID = (c.req.query('list_id') ?? '').trim();
    if (listID) {
      try {
        const list = await resolveList(c.env.DB, listID);
        listID = list.id;
      } catch (err) {
        if (err instanceof NotFoundError) return c.json({ error: 'list not found' }, 404);
        throw err;
      }
    }

    const items = await listContactEngagement(c.env.DB, contactID, listID);
    return c.json({ contact_id: contactID, items });
  });
}
