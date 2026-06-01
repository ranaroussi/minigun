/**
 * TypeScript SDK for MiniGun (https://github.com/ranaroussi/minigun).
 *
 * Zero dependencies — built on the standard `fetch` API, so it runs
 * unchanged in Node 18+, Bun, Deno, Cloudflare Workers, and the
 * browser. Targets ES2020+.
 *
 *   import { Minigun } from './minigun';
 *
 *   const mg = new Minigun(process.env.MINIGUN_API_URL!, process.env.MINIGUN_API_TOKEN!);
 *   await mg.addContact('newsletter', 'alice@example.com', { first_name: 'Alice' });
 *   const res = await mg.sendBulk({
 *     list: 'newsletter',
 *     subject: 'Hi',
 *     from: 'Ran <r@x.com>',
 *     md: "Hello {{first_name | 'there'}}!",
 *   });
 *
 * Every method either resolves with the decoded JSON body or rejects
 * with one of:
 *   - MinigunTransportError — fetch itself threw (DNS, TLS, abort)
 *   - MinigunApiError       — server returned a non-2xx (status + body attached)
 * MinigunError is the common base.
 */

export const UNSUB_LOCAL = 'local' as const;
export const UNSUB_REDIRECT = 'redirect' as const;
export const UNSUB_EXTERNAL = 'external' as const;
export type UnsubMode = typeof UNSUB_LOCAL | typeof UNSUB_REDIRECT | typeof UNSUB_EXTERNAL;

interface Frontmatter {
  /** The Markdown body with any frontmatter block stripped. */
  body: string;
  subject: string;
  preheader: string;
  from: string;
  replyTo: string;
}

/** isFence reports whether a line is a frontmatter delimiter: three or
 * more dashes and nothing else (ignoring surrounding whitespace). */
function isFence(line: string): boolean {
  const s = line.trim();
  return s.length >= 3 && /^-+$/.test(s);
}

function unquote(s: string): string {
  if (s.length >= 2) {
    const a = s[0], b = s[s.length - 1];
    if ((a === '"' && b === '"') || (a === "'" && b === "'")) return s.slice(1, -1);
  }
  return s;
}

/** Extract a leading "---" fenced frontmatter block from a Markdown body.
 * Recognized only when the first non-empty line is a fence (three or more
 * dashes) closed by a later fence line; otherwise the body is returned
 * unchanged. Only subject/preheader/from/reply_to are read; other keys are
 * ignored. The block is always stripped so it never renders into the email. */
function parseFrontmatter(md?: string): Frontmatter {
  const fm: Frontmatter = { body: md ?? '', subject: '', preheader: '', from: '', replyTo: '' };
  if (!md) return fm;

  const src = md.replace(/^\uFEFF/, '');
  const lines = src.split('\n');

  let open = 0;
  while (open < lines.length && lines[open].trim() === '') open++;
  if (open >= lines.length || !isFence(lines[open])) return fm;

  let closing = -1;
  for (let j = open + 1; j < lines.length; j++) {
    if (isFence(lines[j])) { closing = j; break; }
  }
  if (closing < 0) return fm;

  for (const raw of lines.slice(open + 1, closing)) {
    const ln = raw.replace(/\r$/, '');
    const c = ln.indexOf(':');
    if (c < 0) continue;
    const key = ln.slice(0, c).trim().toLowerCase();
    const val = unquote(ln.slice(c + 1).trim());
    switch (key) {
      case 'subject': fm.subject = val; break;
      case 'preheader': fm.preheader = val; break;
      case 'from': fm.from = val; break;
      case 'reply_to':
      case 'reply-to': fm.replyTo = val; break;
    }
  }

  let bodyLines = lines.slice(closing + 1);
  while (bodyLines.length && bodyLines[0].trim() === '') bodyLines = bodyLines.slice(1);
  fm.body = bodyLines.join('\n');
  return fm;
}

function firstNonEmpty(explicit: string | undefined, fallback: string): string {
  return explicit && explicit.trim() !== '' ? explicit : fallback;
}

export class MinigunError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'MinigunError';
  }
}

export class MinigunTransportError extends MinigunError {
  constructor(message: string, public readonly cause?: unknown) {
    super(message);
    this.name = 'MinigunTransportError';
  }
}

export class MinigunApiError extends MinigunError {
  constructor(
    public readonly status: number,
    public readonly body: unknown,
    message: string,
  ) {
    super(message);
    this.name = 'MinigunApiError';
  }
}

export interface MinigunOptions {
  /** Total request timeout in milliseconds (default: 120_000). */
  timeoutMs?: number;
  /** Override the default user-agent. */
  userAgent?: string;
  /**
   * Inject your own fetch (e.g. node-fetch < 18, an undici instance
   * with custom TLS, or a mock during tests). Defaults to globalThis.fetch.
   */
  fetch?: typeof fetch;
}

export interface SendSingleArgs {
  to: string;
  /** Optional if the `md` frontmatter sets `from`; an explicit value wins. */
  from?: string;
  /** Optional if the `md` frontmatter sets `subject`; an explicit value wins. */
  subject?: string;
  company: string;
  md?: string;
  html?: string;
  text?: string;
  template?: string;
  preheader?: string;
  replyTo?: string;
  domain?: string;
  list?: string;
  testMode?: boolean;
  /** RFC3339 future time to schedule the send (e.g. "2026-06-01T09:00:00Z").
   * Omit to send now. Unschedule with cancelSend(). */
  sendAt?: string;
}

export interface SendBulkArgs {
  list: string;
  /** Optional if the `md` frontmatter sets `subject`; an explicit value wins. */
  subject?: string;
  /** Optional if the `md` frontmatter sets `from`; an explicit value wins. */
  from?: string;
  md?: string;
  html?: string;
  text?: string;
  template?: string;
  replyTo?: string;
  preheader?: string;
  domain?: string;
  batchSize?: number;
  throttleMs?: number;
  notifyTo?: string;
  unsubMode?: UnsubMode;
  unsubRedir?: string;
  unsubUrl?: string;
  testMode?: boolean;
  /** RFC3339 future time to schedule the send (e.g. "2026-06-01T09:00:00Z").
   * Omit to send now. Unschedule with cancelSend(). */
  sendAt?: string;
}

export class Minigun {
  private readonly baseUrl: string;
  private readonly token: string;
  private readonly timeoutMs: number;
  private readonly userAgent: string;
  private readonly fetchImpl: typeof fetch;

  constructor(baseUrl: string, token = '', opts: MinigunOptions = {}) {
    if (!baseUrl) throw new Error('baseUrl is required');
    this.baseUrl = baseUrl.replace(/\/+$/, '');
    this.token = token;
    this.timeoutMs = opts.timeoutMs ?? 120_000;
    this.userAgent = opts.userAgent ?? 'minigun-typescript/0.1';
    const f = opts.fetch ?? (globalThis as { fetch?: typeof fetch }).fetch;
    if (!f) {
      throw new Error(
        'No global fetch available — pass `fetch:` in opts (e.g. node-fetch on Node < 18)',
      );
    }
    this.fetchImpl = f;
  }

  // ------------------------------------------------------------------
  // Contacts
  // ------------------------------------------------------------------

  /**
   * Upsert a contact + (re-)subscribe them to a list. Safe to call
   * repeatedly: params get merged and any prior unsubscribe is cleared.
   */
  addContact(list: string, email: string, params?: Record<string, unknown> | null): Promise<unknown> {
    return this.post(`/lists/${enc(list)}/contacts`, {
      email,
      params: params ?? null,
    });
  }

  /**
   * Admin-side unsubscribe by email. Preserves the row with
   * subscribed=0 so future re-imports don't silently re-subscribe.
   * For hard-bounce / spam-complaint cleanup, prefer deleteContact().
   */
  unsubscribeContact(list: string, email: string): Promise<unknown> {
    return this.post(`/lists/${enc(list)}/unsubscribe`, { email });
  }

  /**
   * Permanently purge a contact + every row that references them
   * (subscriptions on every list + unsubscribe-event audit log). Use
   * for hard-bounce / scripted cleanup; the Mailgun webhook does this
   * automatically for inbound bounce / complaint events. Accepts
   * either the contact id (c_XXXX...) or the email.
   */
  deleteContact(idOrEmail: string): Promise<unknown> {
    return this.delete(`/contacts/${enc(idOrEmail)}`);
  }

  /** Paginated subscribers on a list. Pass back `cursor` from the
   * previous response to walk forward. */
  listContacts(list: string, cursor?: string, limit = 50): Promise<unknown> {
    const qs = new URLSearchParams({ limit: String(limit) });
    if (cursor) qs.set('cursor', cursor);
    return this.get(`/lists/${enc(list)}/contacts?${qs.toString()}`);
  }

  // ------------------------------------------------------------------
  // Sends
  // ------------------------------------------------------------------

  /**
   * Send a single transactional email. Required: to, from, subject,
   * company, and one of `md` / `html`. `company` is the id or slug —
   * the sending domain is resolved from it. Pass `domain` to override
   * for this one send. Returns immediately (202); the worker performs
   * the Mailgun POST in the background.
   */
  sendSingle(args: SendSingleArgs): Promise<unknown> {
    if (args.md == null && args.html == null) {
      throw new Error('either md or html is required');
    }
    // Markdown frontmatter fills subject/preheader/from/replyTo when the
    // caller left them empty; the block is stripped from the body.
    const fm = parseFrontmatter(args.md);
    const subject = firstNonEmpty(args.subject, fm.subject);
    const from = firstNonEmpty(args.from, fm.from);
    if (!subject || !from) {
      throw new Error('subject and from are required (pass them or set them in the md frontmatter)');
    }
    return this.post('/send/single', {
      to: args.to,
      from,
      subject,
      preheader: firstNonEmpty(args.preheader, fm.preheader),
      company: args.company,
      list: args.list ?? '',
      reply_to: firstNonEmpty(args.replyTo, fm.replyTo),
      domain: args.domain ?? '',
      md: fm.body,
      html: args.html ?? '',
      text: args.text ?? '',
      template: args.template ?? '',
      test_mode: args.testMode ?? false,
      send_at: args.sendAt ?? '',
    });
  }

  /**
   * Trigger a bulk send. Returns 202 immediately with a send_id
   * while the worker drives batches in the background. The first
   * batch runs inline before the 202 so response time scales with
   * batch_size + Mailgun's latency, then subsequent batches
   * self-chain on the server.
   */
  sendBulk(args: SendBulkArgs): Promise<unknown> {
    if (args.md == null && args.html == null) {
      throw new Error('either md or html is required');
    }
    // Markdown frontmatter fills subject/preheader/from/replyTo when the
    // caller left them empty; the block is stripped from the body.
    const fm = parseFrontmatter(args.md);
    const subject = firstNonEmpty(args.subject, fm.subject);
    const from = firstNonEmpty(args.from, fm.from);
    if (!subject || !from) {
      throw new Error('subject and from are required (pass them or set them in the md frontmatter)');
    }
    const unsubMode: UnsubMode = args.unsubMode ?? UNSUB_LOCAL;
    if (unsubMode !== UNSUB_LOCAL && unsubMode !== UNSUB_REDIRECT && unsubMode !== UNSUB_EXTERNAL) {
      throw new Error("unsubMode must be 'local', 'redirect', or 'external'");
    }
    if (unsubMode === UNSUB_REDIRECT && !args.unsubRedir) {
      throw new Error("unsubRedir is required when unsubMode='redirect'");
    }
    if (unsubMode === UNSUB_EXTERNAL && !args.unsubUrl) {
      throw new Error("unsubUrl is required when unsubMode='external'");
    }

    return this.post('/send/bulk', {
      list: args.list,
      subject,
      from,
      reply_to: firstNonEmpty(args.replyTo, fm.replyTo),
      preheader: firstNonEmpty(args.preheader, fm.preheader),
      domain: args.domain ?? '',
      md: fm.body,
      html: args.html ?? '',
      text: args.text ?? '',
      template: args.template ?? '',
      batch_size: args.batchSize ?? 500,
      throttle_ms: args.throttleMs ?? 1000,
      notify_email: args.notifyTo ?? '',
      unsub_mode: unsubMode,
      unsub_redir: args.unsubRedir ?? '',
      unsub_url: args.unsubUrl ?? '',
      test_mode: args.testMode ?? false,
      send_at: args.sendAt ?? '',
    });
  }

  /** One-shot snapshot of a send's status + per-batch progress. */
  getSend(sendId: string): Promise<unknown> {
    return this.get(`/send/${enc(sendId)}`);
  }

  /** Aggregate stats. DB-backed for completed sends; falls back to a
   * live Mailgun Metrics API call for in-flight / just-finished ones. */
  getSendStats(sendId: string): Promise<unknown> {
    return this.get(`/send/${enc(sendId)}/stats`);
  }

  /** Resume a paused / failed send. Pass force=true ONLY if a batch
   * was left in_flight — Mailgun may already have accepted it, so a
   * retry can duplicate-send. */
  resumeSend(sendId: string, force = false): Promise<unknown> {
    const path = `/send/${enc(sendId)}/resume${force ? '?force=1' : ''}`;
    return this.post(path, {});
  }

  /** Cancel a send that has not started yet (status 'scheduled' or
   * 'queued'), transitioning it to 'cancelled'. This is the unschedule
   * path for sends created with `sendAt`. Rejects (409) if the send is
   * already running or in a terminal state. */
  cancelSend(sendId: string): Promise<unknown> {
    return this.post(`/send/${enc(sendId)}/cancel`, {});
  }

  /** One page of per-recipient message engagement for a send (one row
   * per contact: sent/delivered timestamps, first/last open + click with
   * counts, failure/complaint/unsubscribe state). Keyset-paginated by
   * contact_id. Requires EVENTS_ARCHIVE_ENABLED on the server. */
  listSendRecipients(
    sendId: string,
    opts: {
      limit?: number;
      cursor?: string;
    } = {},
  ): Promise<unknown> {
    const q = new URLSearchParams();
    if (opts.limit && opts.limit > 0) q.set('limit', String(opts.limit));
    if (opts.cursor) q.set('cursor', opts.cursor);
    const qs = q.toString();
    return this.get(`/send/${enc(sendId)}/recipients${qs ? '?' + qs : ''}`);
  }

  /** One page of the per-URL click rollup for a send (one row per
   * contact + clicked link: canonical url, first/last click, click
   * count). Keyset-paginated over (contact_id, url). Requires
   * EVENTS_ARCHIVE_ENABLED on the server. Use to segment an audience by
   * what they clicked. */
  listSendClicks(
    sendId: string,
    opts: {
      limit?: number;
      cursor?: string;
    } = {},
  ): Promise<unknown> {
    const q = new URLSearchParams();
    if (opts.limit && opts.limit > 0) q.set('limit', String(opts.limit));
    if (opts.cursor) q.set('cursor', opts.cursor);
    const qs = q.toString();
    return this.get(`/send/${enc(sendId)}/clicks${qs ? '?' + qs : ''}`);
  }

  /** Per-list engagement counters for one contact. idOrEmail accepts
   * a contact id (c_*) or email. Pass listId to narrow to one list
   * (accepts id or slug). */
  getContactEngagement(idOrEmail: string, listId?: string): Promise<unknown> {
    const path = `/contacts/${enc(idOrEmail)}/engagement`;
    return this.get(listId ? `${path}?list_id=${encodeURIComponent(listId)}` : path);
  }

  /** Unsubscribe dormant contacts from a list. dryRun defaults to TRUE
   * server-side — explicitly pass dryRun=false to commit. At least one
   * criterion must be > 0; multiple criteria are OR'd. Returns
   * {list_id, dry_run, candidates, unsubscribed, sample, reason_counts}. */
  pruneList(opts: {
    list: string;
    minMessagesSinceEngagement?: number;
    dormantForDays?: number;
    noDeliveryForDays?: number;
    dryRun?: boolean;
    limit?: number;
    sampleSize?: number;
  }): Promise<unknown> {
    if (!opts.list) throw new Error('minigun: list is required');
    if (
      !opts.minMessagesSinceEngagement &&
      !opts.dormantForDays &&
      !opts.noDeliveryForDays
    ) {
      throw new Error(
        'minigun: at least one of minMessagesSinceEngagement, dormantForDays, noDeliveryForDays must be > 0',
      );
    }
    const body: Record<string, unknown> = {
      min_messages_since_engagement: opts.minMessagesSinceEngagement ?? 0,
      dormant_for_days: opts.dormantForDays ?? 0,
      no_delivery_for_days: opts.noDeliveryForDays ?? 0,
    };
    if (opts.dryRun !== undefined) body.dry_run = opts.dryRun;
    if (opts.limit) body.limit = opts.limit;
    if (opts.sampleSize) body.sample_size = opts.sampleSize;
    return this.post(`/lists/${enc(opts.list)}/prune`, body);
  }

  // ------------------------------------------------------------------
  // Transport
  // ------------------------------------------------------------------

  private get(path: string): Promise<unknown> {
    return this.request('GET', path, undefined);
  }
  private post(path: string, body: unknown): Promise<unknown> {
    return this.request('POST', path, body);
  }
  private delete(path: string): Promise<unknown> {
    return this.request('DELETE', path, undefined);
  }

  private async request(method: string, path: string, body: unknown): Promise<unknown> {
    const url = this.baseUrl + path;
    const headers: Record<string, string> = {
      Accept: 'application/json',
      'User-Agent': this.userAgent,
    };
    if (this.token) headers.Authorization = 'Bearer ' + this.token;

    let payload: BodyInit | undefined;
    if (body !== undefined) {
      headers['Content-Type'] = 'application/json';
      payload = JSON.stringify(body);
    }

    // AbortController gives us a single timeout that works the same
    // way on Node, Bun, browsers, and Workers — no setTimeout-on-the-
    // socket trick needed.
    const ctrl = new AbortController();
    const timer = setTimeout(() => ctrl.abort(), this.timeoutMs);

    let res: Response;
    try {
      res = await this.fetchImpl(url, {
        method,
        headers,
        body: payload,
        signal: ctrl.signal,
      });
    } catch (e) {
      throw new MinigunTransportError(
        e instanceof Error ? `transport error: ${e.message}` : 'transport error',
        e,
      );
    } finally {
      clearTimeout(timer);
    }

    const raw = await res.text();
    let decoded: unknown = null;
    if (raw) {
      try {
        decoded = JSON.parse(raw);
      } catch {
        decoded = raw;
      }
    }

    if (!res.ok) {
      const msg =
        decoded && typeof decoded === 'object' && 'error' in decoded
          ? String((decoded as { error: unknown }).error)
          : typeof decoded === 'string'
            ? decoded
            : `HTTP ${res.status}`;
      throw new MinigunApiError(res.status, decoded, `MiniGun API ${res.status}: ${msg}`);
    }

    return decoded ?? {};
  }
}

function enc(s: string): string {
  return encodeURIComponent(s);
}
