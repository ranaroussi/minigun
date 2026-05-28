import { Hono } from 'hono';
import { describe, expect, it } from 'vitest';
import type { Env } from '../src/env';
import { mountEvents } from '../src/routes/events';

// Smoke test for the events read surface. We don't exercise the
// DB-touching code path; an empty D1 stub is enough to confirm Hono
// routes the request to the right handler vs. returning the framework's
// default 404.

function fakeDB(): D1Database {
  const noop: any = () => noop;
  const prepared: any = {
    bind: () => prepared,
    all: async () => ({ results: [], success: true, meta: {} }),
    first: async () => null,
    run: async () => ({ meta: {} }),
  };
  const db: any = {
    prepare: () => prepared,
    batch: async () => [],
  };
  return db as D1Database;
}

function newApp() {
  const app = new Hono<{ Bindings: Env }>();
  mountEvents(app);
  return app;
}

describe('events routes', () => {
  // The per-event timeline surface (/send/:id/events) was removed when
  // the raw event ledger was dropped; only the per-recipient rollup
  // remains. With the fake DB the handler returns an empty items list
  // with status 200 — that's the distinguishing signal vs. a routing 404.
  it('GET /send/:id/recipients is mounted and returns an items array', async () => {
    const app = newApp();
    const env = { DB: fakeDB() } as unknown as Env;

    const ok = await app.request('http://x/send/snd_test/recipients', {}, env);
    expect(ok.status).toBe(200);
    const body = (await ok.json()) as { items: unknown[] };
    expect(Array.isArray(body.items)).toBe(true);
  });

  it('GET /send/:id/events is no longer mounted (timeline surface removed)', async () => {
    const app = newApp();
    const env = { DB: fakeDB() } as unknown as Env;

    const gone = await app.request('http://x/send/snd_test/events', {}, env);
    expect(gone.status).toBe(404);
  });
});
