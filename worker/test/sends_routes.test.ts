import { Hono } from 'hono';
import { describe, expect, it } from 'vitest';
import type { Env } from '../src/env';
import { mountSends } from '../src/routes/sends';

// A D1 stub whose getSend (first()) returns a fixed send row and whose
// writes report `changes` so cancelScheduledSend's guarded-update result is
// observable. We only exercise the cancel route's control flow here; the
// store-level scheduling logic is covered by the Go store tests.
function dbReturning(send: Record<string, unknown> | null, changes = 0): D1Database {
  const prepared: any = {
    bind: () => prepared,
    all: async () => ({ results: [], success: true, meta: {} }),
    first: async () => send,
    run: async () => ({ meta: { changes } }),
  };
  const db: any = { prepare: () => prepared, batch: async () => [] };
  return db as D1Database;
}

// Stub for the list-scoped feed: resolveList reads the list via first(), and
// listSends reads the feed via all(). Setting both lets us exercise the
// `GET /sends?list=` control flow (resolve -> filter -> shape).
function dbForSends(
  listRow: Record<string, unknown> | null,
  sendItems: Record<string, unknown>[],
): D1Database {
  const prepared: any = {
    bind: () => prepared,
    all: async () => ({ results: sendItems, success: true, meta: {} }),
    first: async () => listRow,
    run: async () => ({ meta: { changes: 0 } }),
  };
  const db: any = { prepare: () => prepared, batch: async () => [] };
  return db as D1Database;
}

function newApp() {
  const app = new Hono<{ Bindings: Env }>();
  mountSends(app);
  return app;
}

describe('cancel route', () => {
  it('cancels a scheduled send (200 + status cancelled)', async () => {
    const app = newApp();
    const env = { DB: dbReturning({ id: 'snd_1', status: 'scheduled' }, 1) } as unknown as Env;

    const res = await app.request('http://x/send/snd_1/cancel', { method: 'POST' }, env);
    expect(res.status).toBe(200);
    const body = (await res.json()) as { status: string };
    expect(body.status).toBe('cancelled');
  });

  it('refuses to cancel a running send (409)', async () => {
    const app = newApp();
    const env = { DB: dbReturning({ id: 'snd_2', status: 'running' }, 0) } as unknown as Env;

    const res = await app.request('http://x/send/snd_2/cancel', { method: 'POST' }, env);
    expect(res.status).toBe(409);
  });

  it('404s when the send does not exist', async () => {
    const app = newApp();
    const env = { DB: dbReturning(null) } as unknown as Env;

    const res = await app.request('http://x/send/missing/cancel', { method: 'POST' }, env);
    expect(res.status).toBe(404);
    const body = (await res.json()) as { error: string };
    expect(body.error).toBe('send not found');
  });
});

describe('sends list route', () => {
  it('filters by list (slug or id) and returns its sends', async () => {
    const app = newApp();
    const env = {
      DB: dbForSends({ id: 'l_1', slug: 'ranaroussi' }, [
        { id: 's_1', list_id: 'l_1', created_at: '2026-08-13T11:40:00.000Z' },
      ]),
    } as unknown as Env;

    const res = await app.request('http://x/sends?limit=1&list=ranaroussi', {}, env);
    expect(res.status).toBe(200);
    const body = (await res.json()) as { items: { id: string }[] };
    expect(body.items.length).toBe(1);
    expect(body.items[0]?.id).toBe('s_1');
  });

  it('404s when the list filter names an unknown list', async () => {
    const app = newApp();
    const env = { DB: dbForSends(null, []) } as unknown as Env;

    const res = await app.request('http://x/sends?list=nope', {}, env);
    expect(res.status).toBe(404);
    const body = (await res.json()) as { error: string };
    expect(body.error).toBe('list not found');
  });

  it('returns an empty feed for a list that has no sends', async () => {
    const app = newApp();
    const env = { DB: dbForSends({ id: 'l_2', slug: 'empty' }, []) } as unknown as Env;

    const res = await app.request('http://x/sends?list=empty', {}, env);
    expect(res.status).toBe(200);
    const body = (await res.json()) as { items: unknown[] };
    expect(body.items.length).toBe(0);
  });
});
