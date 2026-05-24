import { Hono } from 'hono';
import { Env } from '../env';

export function mountHealth(app: Hono<{ Bindings: Env }>) {
  app.get('/healthz', async (c) => {
    try {
      await c.env.DB.prepare('SELECT 1').first();
      return c.json({ ok: true });
    } catch (err) {
      return c.json({ ok: false, error: (err as Error).message }, 500);
    }
  });
}
