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
