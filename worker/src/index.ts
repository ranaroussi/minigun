import { Hono } from 'hono';
import { Env } from './env';
import { bearerAuth } from './routes/auth';
import { mountCompanies } from './routes/companies';
import { mountContacts } from './routes/contacts';
import { mountEvents } from './routes/events';
import { mountHealth } from './routes/health';
import { mountLists } from './routes/lists';
import { mountManage } from './routes/manage';
import { mountSends } from './routes/sends';
import { mountUnsubscribe } from './routes/unsubscribe';
import { mountWebhooks } from './routes/webhooks';
import { sweepStuckSends } from './send/cron';
import { pullDueSendEvents } from './send/events_pull';
import { refreshDueStats } from './send/stats';

const app = new Hono<{ Bindings: Env }>();

app.use('*', async (c, next) => {
  c.res.headers.set('x-powered-by', 'minigun-worker');
  await next();
});
app.use('*', bearerAuth);

app.get('/', (c) => {
  const url = c.env.REDIRECT_URL;
  if (url) return c.redirect(url, 302);
  return c.json({ error: 'not found' }, 404);
});

mountHealth(app);
mountCompanies(app);
mountLists(app);
mountContacts(app);
mountSends(app);
mountEvents(app);
mountUnsubscribe(app);
mountManage(app);
mountWebhooks(app);

app.notFound((c) => c.json({ error: 'not found' }, 404));
app.onError((err, c) => {
  console.error('unhandled', err);
  return c.json({ error: err.message || 'internal error' }, 500);
});

export default {
  fetch: app.fetch,
  async scheduled(_event: ScheduledEvent, env: Env, ctx: ExecutionContext): Promise<void> {
    ctx.waitUntil(sweepStuckSends(env));
    ctx.waitUntil(refreshDueStats(env));
    // pullDueSendEvents internally short-circuits when EVENTS_ARCHIVE_ENABLED
    // is not "true", so this is a near-zero-cost noop until Phase 2 is
    // activated by setting the flag.
    ctx.waitUntil(pullDueSendEvents(env));
  },
};
