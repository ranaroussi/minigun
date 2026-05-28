import { Hono } from 'hono';
import { describe, expect, it } from 'vitest';
import type { Env } from '../src/env';
import { mountEvents } from '../src/routes/events';

// Smoke test for the Phase 5 route-path fix. Phase 3 mistakenly mounted
// /sends/:id/events while Go, the CLI, MCP, and all 4 SDKs were calling
// /send/{id}/events — meaning the Worker read surface 404'd everywhere.
// These tests pin the canonical paths so the parity regression can't
// recur silently.
//
// We don't exercise the DB-touching code path; an empty D1 stub is
// enough to confirm Hono routes the request to the right handler vs.
// returning the framework's default 404.

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

describe('events routes — Phase 5 path fix', () => {
  // Phase 3 mistakenly mounted the events read route at /sends/:id/events
  // (plural) while every other surface (Go, CLI, MCP, all 4 SDKs) called
  // /send/{id}/events (singular). On a Worker-backed deployment this
  // meant the entire read surface 404'd. Phase 5 realigned the Worker
  // route to the singular form. This test pins the canonical path so the
  // parity regression can't recur silently.
  //
  // We don't exercise the DB-touching code path; an empty D1 stub is
  // enough to confirm Hono routes the request to the handler vs.
  // returning the framework's default 404. With the fake DB the handler
  // returns an empty events list with status 200 — that's the
  // distinguishing signal.
  it('GET /send/:id/events is mounted on the singular path', async () => {
    const app = newApp();
    const env = { DB: fakeDB() } as unknown as Env;

    const ok = await app.request('http://x/send/snd_test/events', {}, env);
    expect(ok.status).toBe(200);
    const body = (await ok.json()) as { items: unknown[] };
    expect(Array.isArray(body.items)).toBe(true);
  });

  it('GET /sends/:id/events (plural) returns 404 — the Phase 3 path must NOT be mounted', async () => {
    const app = newApp();
    const env = { DB: fakeDB() } as unknown as Env;

    const old = await app.request('http://x/sends/snd_test/events', {}, env);
    expect(old.status).toBe(404);
  });
});
