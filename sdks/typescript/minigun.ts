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
  from: string;
  subject: string;
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
}

export interface SendBulkArgs {
  list: string;
  subject: string;
  from: string;
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
    return this.post('/send/single', {
      to: args.to,
      from: args.from,
      subject: args.subject,
      preheader: args.preheader ?? '',
      company: args.company,
      list: args.list ?? '',
      reply_to: args.replyTo ?? '',
      domain: args.domain ?? '',
      md: args.md ?? '',
      html: args.html ?? '',
      text: args.text ?? '',
      template: args.template ?? '',
      test_mode: args.testMode ?? false,
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
      subject: args.subject,
      from: args.from,
      reply_to: args.replyTo ?? '',
      preheader: args.preheader ?? '',
      domain: args.domain ?? '',
      md: args.md ?? '',
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

  /** One page of archived Mailgun events for a send (requires
   * EVENTS_ARCHIVE_ENABLED on the server). Response shape:
   * { items: [...], next_cursor?: string }. When next_cursor is
   * absent, the page is the last one. */
  listSendEvents(
    sendId: string,
    opts: {
      event?: string;
      sinceMs?: number;
      limit?: number;
      cursor?: string;
    } = {},
  ): Promise<unknown> {
    const q = new URLSearchParams();
    if (opts.event) q.set('event', opts.event);
    if (opts.sinceMs && opts.sinceMs > 0) q.set('since', String(opts.sinceMs));
    if (opts.limit && opts.limit > 0) q.set('limit', String(opts.limit));
    if (opts.cursor) q.set('cursor', opts.cursor);
    const qs = q.toString();
    return this.get(`/send/${enc(sendId)}/events${qs ? '?' + qs : ''}`);
  }

  /** Per-list engagement counters for one contact. idOrEmail accepts
   * a contact id (c_*) or email. Pass listId to narrow to one list
   * (accepts id or slug). */
  getContactEngagement(idOrEmail: string, listId?: string): Promise<unknown> {
    const path = `/contacts/${enc(idOrEmail)}/engagement`;
    return this.get(listId ? `${path}?list_id=${encodeURIComponent(listId)}` : path);
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
