import { Context, Hono } from 'hono';
import { Env, publicURL } from '../env';
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
import { resolveList } from '../store/lists';
import { createSend, getSend, listSends } from '../store/sends';
import { getSendStats } from '../store/stats';
import {
  countSubscribed,
  maxSubscriptionID,
} from '../store/subscriptions';
import { NotFoundError, UnsubscribeMode } from '../store/types';
import { countUnsubscribesForSend } from '../store/unsubs';

function emptyToNull(s: string | undefined | null): string | null {
  if (!s || !s.trim()) return null;
  return s;
}

function kick(env: Env, ctx: ExecutionContext, sendID: string): void {
  ctx.waitUntil(
    fetch(`${publicURL(env)}/send/${sendID}/next`, {
      method: 'POST',
      headers: { 'x-internal-secret': env.MINIGUN_INTERNAL_SECRET },
    }).catch((err) => console.error('initial kick failed', sendID, err)),
  );
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
    }>().catch(() => null);
    if (!body) return c.json({ error: 'invalid JSON' }, 400);
    if (!body.list?.trim() || !body.subject?.trim() || !body.from?.trim()) {
      return c.json({ error: 'list, subject, and from are required' }, 400);
    }
    if (!body.md && !body.html) {
      return c.json({ error: 'either md or html is required' }, 400);
    }
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
      max_subscription_id: maxID,
      total_recipients: total,
      unsubscribe_mode: mode,
      unsubscribe_redirect_url: emptyToNull(body.unsub_redir),
      unsubscribe_external_url: emptyToNull(body.unsub_url),
      notify_email: emptyToNull(body.notify_email),
    });
    kick(c.env, c.executionCtx, snd.id);
    return c.json({ send_id: snd.id, status: snd.status, total_recipients: total }, 202);
  });

  app.post('/send/single', async (c) => {
    const body = await c.req.json<{
      to?: string;
      from?: string;
      reply_to?: string;
      subject?: string;
      company?: string;
      domain?: string;
      md?: string;
      html?: string;
      text?: string;
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

    let bodyHTML: string;
    let bodyText: string;
    if (body.md) {
      const built = buildBody(body.md, '', body.subject, '');
      bodyHTML = built.html;
      bodyText = built.text;
    } else {
      const htmlWithFooter = ensureUnsubFooterHTML(body.html!);
      const { rewritten } = rewriteVariables(htmlWithFooter);
      bodyHTML = rewritten;
      const baseText = body.text ?? htmlToText(body.html!);
      bodyText = rewriteVariables(ensureUnsubFooterText(baseText)).rewritten;
    }

    const snd = await createSend(c.env.DB, {
      type: 'single',
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
    });
    c.executionCtx.waitUntil(runSingle(c.env, snd.id));
    return c.json({ send_id: snd.id, status: snd.status }, 202);
  });

  app.post('/send/:id/next', handleSendStep);
  app.post('/send/:id/resume', handleSendStep);

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
