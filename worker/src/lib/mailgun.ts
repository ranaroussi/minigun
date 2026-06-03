import { Env, mailgunApiBase } from '../env';

export type Message = {
  domain: string;
  from: string;
  to: string[];
  subject: string;
  html?: string;
  text?: string;
  replyTo?: string;
  tag?: string;
  trackingOpens: boolean;
  trackingClicks: boolean;
  trackingUnsubscribeOn: boolean;
  listUnsubscribe?: string;
  listUnsubscribePost?: string;
  recipientVariables?: Record<string, Record<string, unknown>>;
  customVars?: Record<string, string>;
  testMode?: boolean;
};

export type SendResponse = {
  id?: string;
  message?: string;
};

export class MailgunAPIError extends Error {
  statusCode: number;
  body: string;
  constructor(statusCode: number, body: string) {
    super(`mailgun api error: status=${statusCode} body=${body}`);
    this.name = 'MailgunAPIError';
    this.statusCode = statusCode;
    this.body = body;
  }
  retryable(): boolean {
    return this.statusCode === 429 || (this.statusCode >= 500 && this.statusCode <= 599);
  }
}

function basicAuth(apiKey: string): string {
  return 'Basic ' + btoa('api:' + apiKey);
}

export async function sendMessage(env: Env, m: Message): Promise<SendResponse> {
  if (!m.domain) throw new Error('mailgun: message.domain is required');
  const form = new FormData();
  form.append('from', m.from);
  // Explicitly set the RFC 5322 Sender header to match From. Without this,
  // when From.domain differs from the sending domain Mailgun synthesizes a
  // VERP-style Sender (e.g. "user=apex.com@subdomain.com") for bounce
  // routing and Gmail/Apple Mail surface it in the visible message UI.
  // RFC 5322 §3.6.2 says when Sender == From, well-behaved clients may
  // omit Sender from display entirely, so this is harmless even when the
  // domains already align.
  form.append('h:Sender', m.from);
  for (const to of m.to) form.append('to', to);
  form.append('subject', m.subject);
  if (m.text) form.append('text', m.text);
  if (m.html) form.append('html', m.html);
  if (m.replyTo) form.append('h:Reply-To', m.replyTo);
  if (m.tag) form.append('o:tag', m.tag);
  form.append('o:tracking', 'yes');
  form.append('o:tracking-opens', m.trackingOpens ? 'yes' : 'no');
  form.append('o:tracking-clicks', m.trackingClicks ? 'yes' : 'no');
  form.append('o:tracking-unsubscribe', m.trackingUnsubscribeOn ? 'yes' : 'no');
  if (m.listUnsubscribe) form.append('h:List-Unsubscribe', m.listUnsubscribe);
  if (m.listUnsubscribePost) form.append('h:List-Unsubscribe-Post', m.listUnsubscribePost);
  if (m.recipientVariables && Object.keys(m.recipientVariables).length > 0) {
    form.append('recipient-variables', JSON.stringify(m.recipientVariables));
  }
  if (m.customVars) {
    for (const [k, v] of Object.entries(m.customVars)) form.append('v:' + k, v);
  }
  if (m.testMode) form.append('o:testmode', 'yes');

  const endpoint = `${mailgunApiBase(env)}/v3/${m.domain}/messages`;
  const resp = await fetch(endpoint, {
    method: 'POST',
    headers: { Authorization: basicAuth(env.MAILGUN_API_KEY) },
    body: form,
  });
  const body = await resp.text();
  if (!resp.ok) throw new MailgunAPIError(resp.status, body);
  try {
    return JSON.parse(body) as SendResponse;
  } catch {
    return { message: body };
  }
}

export async function sendMessageWithRetry(
  env: Env,
  m: Message,
  maxAttempts = 5,
): Promise<SendResponse> {
  let lastErr: unknown;
  for (let attempt = 1; attempt <= maxAttempts; attempt++) {
    try {
      return await sendMessage(env, m);
    } catch (err) {
      lastErr = err;
      if (err instanceof MailgunAPIError && !err.retryable()) throw err;
      if (attempt === maxAttempts) break;
      const backoff = Math.min(1000 * 2 ** attempt, 30000) + Math.floor(Math.random() * 500);
      await new Promise((r) => setTimeout(r, backoff));
    }
  }
  throw lastErr;
}

export type MetricsResponse = {
  items: { dimensions: string[]; metrics: Record<string, number> }[];
};

export type PerSendTotals = {
  sent: number;
  delivered: number;
  opened: number;
  clicked: number;
  failed: number;
  complained: number;
};

export async function perSendMetrics(
  env: Env,
  sendID: string,
  sendCreatedAt: Date,
): Promise<PerSendTotals> {
  const start = new Date(sendCreatedAt.getTime() - 60 * 60 * 1000);
  const end = new Date(Date.now() + 60 * 60 * 1000);
  const resp = await metrics(
    env,
    start,
    end,
    [
      'accepted_count',
      'delivered_count',
      'failed_count',
      'opened_count',
      'clicked_count',
      'complained_count',
    ],
    sendID,
  );
  const totals: PerSendTotals = {
    sent: 0,
    delivered: 0,
    opened: 0,
    clicked: 0,
    failed: 0,
    complained: 0,
  };
  for (const item of resp.items ?? []) {
    totals.sent += item.metrics['accepted_count'] ?? 0;
    totals.delivered += item.metrics['delivered_count'] ?? 0;
    totals.opened += item.metrics['opened_count'] ?? 0;
    totals.clicked += item.metrics['clicked_count'] ?? 0;
    totals.failed += item.metrics['failed_count'] ?? 0;
    totals.complained += item.metrics['complained_count'] ?? 0;
  }
  return totals;
}

// ---------------------------------------------------------------------------
// Events API
// ---------------------------------------------------------------------------

// Raw event shape returned by Mailgun's GET /v3/<domain>/events. Mailgun's
// API is loosely-typed (event-specific fields appear or vanish by event
// type). We only type the fields the engagement rollups consume; the
// forensic/variable-shape fields (message, client-info, geolocation,
// user-variables) are left to the index signature since MiniGun keeps no
// raw event log — events fold straight into the rollups.
export type MailgunEventRaw = {
  id: string;
  event: string;
  timestamp: number;
  recipient?: string;
  severity?: string;
  reason?: string;
  url?: string;
  tags?: string[];
  [k: string]: unknown;
};

export type EventsPage = {
  items: MailgunEventRaw[];
  paging?: {
    next?: string;
    previous?: string;
    first?: string;
    last?: string;
  };
};

// Fetch one page of events for a domain. When `tag` is set we filter to
// events whose o:tag matches (i.e. all events for one MiniGun send_id).
// When `pageURL` is set we follow Mailgun's cursor directly — this is how
// pagination works (the next page URL comes back in the previous response).
export async function fetchEvents(
  env: Env,
  args:
    | { domain: string; tag?: string; beginMs?: number; endMs?: number; limit?: number }
    | { pageURL: string },
): Promise<EventsPage> {
  let url: string;
  if ('pageURL' in args) {
    url = args.pageURL;
  } else {
    const params = new URLSearchParams();
    params.set('ascending', 'yes');
    params.set('limit', String(args.limit ?? 300));
    if (args.tag) params.set('tags', args.tag);
    // Mailgun's events API accepts begin/end as RFC 2822 strings OR floats
    // (epoch seconds). Floats are more robust across regions.
    if (args.beginMs !== undefined) params.set('begin', String(args.beginMs / 1000));
    if (args.endMs !== undefined) params.set('end', String(args.endMs / 1000));
    url = `${mailgunApiBase(env)}/v3/${args.domain}/events?${params.toString()}`;
  }
  const resp = await fetch(url, {
    method: 'GET',
    headers: { Authorization: basicAuth(env.MAILGUN_API_KEY) },
  });
  const body = await resp.text();
  if (!resp.ok) throw new MailgunAPIError(resp.status, body);
  try {
    return JSON.parse(body) as EventsPage;
  } catch {
    return { items: [] };
  }
}

export async function metrics(
  env: Env,
  start: Date,
  end: Date,
  metricsList: string[],
  tag?: string,
): Promise<MetricsResponse> {
  const body: Record<string, unknown> = {
    // Mailgun's analytics-metrics endpoint rejects ISO-8601; it wants an
    // RFC 2822 / RFC 1123 date (e.g. "Mon, 02 Jun 2026 09:58:03 GMT"), which
    // is exactly what Date.toUTCString() produces.
    start: start.toUTCString(),
    end: end.toUTCString(),
    resolution: 'day',
    metrics: metricsList,
  };
  if (tag) {
    body['filter'] = {
      AND: [
        {
          attribute: 'tag',
          comparator: '=',
          values: [{ label: tag, value: tag }],
        },
      ],
    };
  }
  const endpoint = `${mailgunApiBase(env)}/v1/analytics/metrics`;
  const resp = await fetch(endpoint, {
    method: 'POST',
    headers: {
      Authorization: basicAuth(env.MAILGUN_API_KEY),
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(body),
  });
  const text = await resp.text();
  if (!resp.ok) throw new MailgunAPIError(resp.status, text);
  try {
    return JSON.parse(text) as MetricsResponse;
  } catch {
    return { items: [] };
  }
}
