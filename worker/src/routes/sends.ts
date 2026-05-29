import { Context, Hono } from 'hono';
import { Env } from '../env';
import {
  buildBody,
  ensureUnsubFooterHTML,
  ensureUnsubFooterText,
  htmlToText,
  rewriteVariables,
} from '../lib/markdown';
import { PerSendTotals, perSendMetrics } from '../lib/mailgun';
import { clampLimit, decodeCursor, encodeCursor } from '../lib/pagination';
import { scheduleNextStep, step } from '../send/bulk';
import { runSingle } from '../send/single';
import { sendProgress } from '../store/batches';
import { resolveCompany } from '../store/companies';
import { upsertContact } from '../store/contacts';
import { resolveList } from '../store/lists';
import { cancelScheduledSend, createSend, getSend, listSends } from '../store/sends';
import { getSendStats } from '../store/stats';
import {
  countSubscribed,
  maxSubscriptionID,
  upsertSubscription,
} from '../store/subscriptions';
import { NotFoundError, UnsubscribeMode } from '../store/types';
import { countUnsubscribesForSend } from '../store/unsubs';

function emptyToNull(s: string | undefined | null): string | null {
  if (!s || !s.trim()) return null;
  return s;
}

// Returns an error message when send_at is present but unparseable, else null
// (null = valid, which includes the "not provided" case). Empty means send now.
function sendAtError(s: string | undefined | null): string | null {
  if (!s || !s.trim()) return null;
  if (Number.isNaN(Date.parse(s.trim()))) {
    return 'send_at must be an ISO-8601 timestamp (e.g. 2026-06-01T09:00:00Z)';
  }
  return null;
}

async function handleSendStep(c: Context<{ Bindings: Env }>) {
  const id = c.req.param('id');
  if (!id) return c.json({ error: 'send id required' }, 400);
  let snd;
  try {
    snd = await getSend(c.env.DB, id);
  } catch (err) {
    if (err instanceof NotFoundError) return c.json({ error: 'send not found' }, 404);
    throw err;
  }
  if (snd.status === 'completed' || snd.status === 'cancelled' || snd.status === 'failed') {
    return c.json({ state: 'terminal', status: snd.status });
  }
  if (snd.type === 'single') {
    c.executionCtx.waitUntil(runSingle(c.env, id));
    return c.json({ state: 'started' });
  }
  const result = await step(c.env, id);
  if (result.state === 'sent') {
    scheduleNextStep(c.env, c.executionCtx, id, snd.throttle_ms);
  }
  return c.json(result);
}

export function mountSends(app: Hono<{ Bindings: Env }>) {
  app.post('/send/bulk', async (c) => {
    const body = await c.req.json<{
      list?: string;
      subject?: string;
      preheader?: string;
      from?: string;
      reply_to?: string;
      domain?: string;
      md?: string;
      html?: string;
      text?: string;
      template?: string;
      batch_size?: number;
      throttle_ms?: number;
      unsub_mode?: string;
      unsub_redir?: string;
      unsub_url?: string;
      notify_email?: string;
      test_mode?: boolean;
      send_at?: string;
    }>().catch(() => null);
    if (!body) return c.json({ error: 'invalid JSON' }, 400);
    if (!body.list?.trim() || !body.subject?.trim() || !body.from?.trim()) {
      return c.json({ error: 'list, subject, and from are required' }, 400);
    }
    if (!body.md && !body.html) {
      return c.json({ error: 'either md or html is required' }, 400);
    }
    const bulkSendAtErr = sendAtError(body.send_at);
    if (bulkSendAtErr) return c.json({ error: bulkSendAtErr }, 400);
    let list;
    try {
      list = await resolveList(c.env.DB, body.list);
    } catch (err) {
      if (err instanceof NotFoundError) return c.json({ error: 'list not found' }, 404);
      throw err;
    }
    const sendingDomain = (body.domain ?? '').trim().toLowerCase() || list.sending_domain;
    if (!sendingDomain) {
      return c.json(
        { error: 'list has no sending_domain configured; pass "domain" to override or fix the list' },
        400,
      );
    }

    let bodyHTML: string;
    let bodyText: string;
    if (body.md) {
      const built = buildBody(body.md, '', body.subject, body.preheader ?? '');
      bodyHTML = built.html;
      bodyText = built.text;
    } else {
      const htmlWithFooter = ensureUnsubFooterHTML(body.html!);
      const { rewritten } = rewriteVariables(htmlWithFooter);
      bodyHTML = rewritten;
      if (body.text) {
        bodyText = rewriteVariables(ensureUnsubFooterText(body.text)).rewritten;
      } else {
        const derivedText = ensureUnsubFooterText(htmlToText(body.html!));
        bodyText = rewriteVariables(derivedText).rewritten;
      }
    }

    const maxID = await maxSubscriptionID(c.env.DB, list.id);
    const total = await countSubscribed(c.env.DB, list.id, maxID);
    const mode = (body.unsub_mode || 'local') as UnsubscribeMode;

    // A scheduled bulk send resolves its audience at dispatch (so everyone
    // subscribed up to go-time is included), not at creation. Park it with a
    // null max_subscription_id; total here is just a live estimate.
    const scheduled = !!body.send_at && new Date(body.send_at).getTime() > Date.now();

    const snd = await createSend(c.env.DB, {
      type: 'bulk',
      list_id: list.id,
      subject: body.subject,
      from_header: body.from,
      reply_to: emptyToNull(body.reply_to),
      template_name: emptyToNull(body.template),
      body_md: emptyToNull(body.md ?? null),
      body_html: bodyHTML,
      body_text: bodyText,
      sending_domain: sendingDomain,
      batch_size: body.batch_size,
      throttle_ms: body.throttle_ms,
      max_subscription_id: scheduled ? null : maxID,
      total_recipients: total,
      unsubscribe_mode: mode,
      unsubscribe_redirect_url: emptyToNull(body.unsub_redir),
      unsubscribe_external_url: emptyToNull(body.unsub_url),
      notify_email: emptyToNull(body.notify_email),
      test_mode: body.test_mode === true,
      send_at: body.send_at,
    });
    // A future-dated send is parked for the cron dispatcher; don't kick it.
    if (snd.status === 'scheduled') {
      return c.json(
        { send_id: snd.id, status: snd.status, send_at: snd.send_at, total_recipients: total },
        202,
      );
    }
    // Run the first batch inline rather than self-fetching /send/:id/next.
    // The fire-and-forget kick was unreliable on this worker (waitUntil could
    // drop the in-flight subrequest before it landed), leaving sends stuck in
    // 'queued' until the cron sweep picked them up. Executing step() here
    // guarantees the chain has actually started before we return 202.
    // Subsequent batches still ride the scheduleNextStep self-fetch chain;
    // the cron sweep is the safety net if that drops.
    let respStatus: typeof snd.status = snd.status;
    try {
      const result = await step(c.env, snd.id);
      if (result.state === 'sent') {
        respStatus = 'running';
        scheduleNextStep(c.env, c.executionCtx, snd.id, snd.throttle_ms);
      } else if (result.state === 'completed') {
        respStatus = 'completed';
      }
    } catch (err) {
      console.error('initial step failed', snd.id, err);
    }
    return c.json({ send_id: snd.id, status: respStatus, total_recipients: total }, 202);
  });

  app.post('/send/single', async (c) => {
    const body = await c.req.json<{
      to?: string;
      from?: string;
      reply_to?: string;
      subject?: string;
      preheader?: string;
      company?: string;
      list?: string;
      domain?: string;
      md?: string;
      html?: string;
      text?: string;
      template?: string;
      test_mode?: boolean;
      send_at?: string;
    }>().catch(() => null);
    if (!body) return c.json({ error: 'invalid JSON' }, 400);
    if (!body.to?.trim() || !body.subject?.trim() || !body.from?.trim()) {
      return c.json({ error: 'to, subject, and from are required' }, 400);
    }
    if (!body.company?.trim()) {
      return c.json({ error: 'company is required (id or slug) to resolve sending domain' }, 400);
    }
    if (!body.md && !body.html) {
      return c.json({ error: 'either md or html is required' }, 400);
    }
    const singleSendAtErr = sendAtError(body.send_at);
    if (singleSendAtErr) return c.json({ error: singleSendAtErr }, 400);

    let company;
    try {
      company = await resolveCompany(c.env.DB, body.company.trim());
    } catch (err) {
      if (err instanceof NotFoundError) return c.json({ error: 'company not found' }, 404);
      throw err;
    }
    const sendingDomain = (body.domain ?? '').trim().toLowerCase() || company.sending_domain;
    if (!sendingDomain) {
      return c.json(
        { error: 'company has no sending_domain configured; pass "domain" to override or fix the company' },
        400,
      );
    }

    // If the caller tied this transactional send to a list, upsert the
    // recipient's contact + subscription so we can mint a real
    // per-recipient unsubscribe token at send time. No list = no opt-out.
    let listIDForSend: string | null = null;
    let subscriptionID = 0;
    if (body.list?.trim()) {
      let resolvedList;
      try {
        resolvedList = await resolveList(c.env.DB, body.list.trim());
      } catch (err) {
        if (err instanceof NotFoundError) return c.json({ error: 'list not found' }, 404);
        throw err;
      }
      listIDForSend = resolvedList.id;
      const contact = await upsertContact(c.env.DB, body.to, undefined);
      const sub = await upsertSubscription(c.env.DB, resolvedList.id, contact.id);
      subscriptionID = sub.id;
    }
    const autoInjectUnsub = subscriptionID > 0;

    let bodyHTML: string;
    let bodyText: string;
    if (body.md) {
      const built = buildBody(body.md, body.template ?? '', body.subject, body.preheader ?? '', autoInjectUnsub);
      bodyHTML = built.html;
      bodyText = built.text;
    } else {
      let htmlSrc = body.html!;
      let textSrc = body.text ?? htmlToText(body.html!);
      if (autoInjectUnsub) {
        htmlSrc = ensureUnsubFooterHTML(htmlSrc);
        textSrc = ensureUnsubFooterText(textSrc);
      }
      bodyHTML = rewriteVariables(htmlSrc).rewritten;
      bodyText = rewriteVariables(textSrc).rewritten;
    }

    const snd = await createSend(c.env.DB, {
      type: 'single',
      list_id: listIDForSend,
      recipient_email: body.to,
      subject: body.subject,
      from_header: body.from,
      reply_to: emptyToNull(body.reply_to),
      body_md: emptyToNull(body.md ?? null),
      body_html: bodyHTML,
      body_text: bodyText,
      sending_domain: sendingDomain,
      batch_size: 1,
      throttle_ms: 0,
      test_mode: body.test_mode === true,
      last_subscription_id: subscriptionID,
      send_at: body.send_at,
    });
    if (snd.status === 'scheduled') {
      return c.json({ send_id: snd.id, status: snd.status, send_at: snd.send_at }, 202);
    }
    c.executionCtx.waitUntil(runSingle(c.env, snd.id));
    return c.json({ send_id: snd.id, status: snd.status }, 202);
  });

  app.post('/send/:id/next', handleSendStep);
  app.post('/send/:id/resume', handleSendStep);

  app.post('/send/:id/cancel', async (c) => {
    const id = c.req.param('id');
    if (!id) return c.json({ error: 'send id required' }, 400);
    let snd;
    try {
      snd = await getSend(c.env.DB, id);
    } catch (err) {
      if (err instanceof NotFoundError) return c.json({ error: 'send not found' }, 404);
      throw err;
    }
    if (snd.status === 'running') {
      return c.json({ error: 'send is already running; cannot cancel an in-flight send' }, 409);
    }
    if (snd.status === 'completed' || snd.status === 'failed' || snd.status === 'cancelled') {
      return c.json({ error: `send is already ${snd.status}` }, 409);
    }
    const cancelled = await cancelScheduledSend(c.env.DB, id);
    if (!cancelled) {
      return c.json({ error: 'send started before it could be cancelled' }, 409);
    }
    return c.json({ send_id: id, status: 'cancelled' });
  });

  app.get('/send/:id', async (c) => {
    const id = c.req.param('id');
    let snd;
    try {
      snd = await getSend(c.env.DB, id);
    } catch (err) {
      if (err instanceof NotFoundError) return c.json({ error: 'send not found' }, 404);
      throw err;
    }
    const { completed, sent } = await sendProgress(c.env.DB, id);
    const totalBatches =
      snd.batch_size > 0 && snd.total_recipients > 0
        ? Math.ceil(snd.total_recipients / snd.batch_size)
        : 0;
    const remaining = Math.max(0, snd.total_recipients - sent);
    return c.json({
      id: snd.id,
      status: snd.status,
      progress: {
        completed_batches: completed,
        total_batches: totalBatches,
        sent,
        remaining,
        last_subscription_id: snd.last_subscription_id,
      },
      send_at: snd.send_at,
      created_at: snd.created_at,
      updated_at: snd.updated_at,
      completed_at: snd.completed_at,
      last_error: snd.last_error,
    });
  });

  app.get('/send/:id/stats', async (c) => {
    const id = c.req.param('id');
    let snd;
    try {
      snd = await getSend(c.env.DB, id);
    } catch (err) {
      if (err instanceof NotFoundError) return c.json({ error: 'send not found' }, 404);
      throw err;
    }

    const st = await getSendStats(c.env.DB, id);
    if (st && (st.is_final || st.last_fetched_at)) {
      return c.json({
        id: snd.id,
        sent: st.sent,
        delivered: st.delivered,
        opened: st.opened,
        clicked: st.clicked,
        failed: st.failed,
        complained: st.complained,
        unsubscribed: st.unsubscribed,
        is_final: st.is_final,
        last_fetched_at: st.last_fetched_at,
        source: 'send_stats',
      });
    }

    let totals: PerSendTotals = {
      sent: 0,
      delivered: 0,
      opened: 0,
      clicked: 0,
      failed: 0,
      complained: 0,
    };
    try {
      totals = await perSendMetrics(c.env, snd.id, new Date(snd.created_at));
    } catch (err) {
      console.warn('mailgun metrics failed', snd.id, err);
    }
    const unsub = st ? st.unsubscribed : await countUnsubscribesForSend(c.env.DB, id);
    return c.json({
      id: snd.id,
      sent: totals.sent,
      delivered: totals.delivered,
      opened: totals.opened,
      clicked: totals.clicked,
      failed: totals.failed,
      complained: totals.complained,
      unsubscribed: unsub,
      is_final: false,
      source: 'mailgun_live',
    });
  });

  app.get('/sends', async (c) => {
    let cursor;
    try {
      cursor = decodeCursor(c.req.query('cursor'));
    } catch {
      return c.json({ error: 'invalid cursor' }, 400);
    }
    const limit = clampLimit(Number(c.req.query('limit') ?? '0'));
    const items = await listSends(
      c.env.DB,
      cursor.afterCreated ?? '',
      cursor.afterStringID ?? '',
      limit + 1,
    );
    const hasMore = items.length > limit;
    const trimmed = hasMore ? items.slice(0, limit) : items;
    const last = trimmed[trimmed.length - 1];
    const nextCursor =
      hasMore && last
        ? encodeCursor({ afterCreated: last.created_at, afterStringID: last.id })
        : '';
    return c.json({ items: trimmed, next_cursor: nextCursor, has_more: hasMore });
  });
}
