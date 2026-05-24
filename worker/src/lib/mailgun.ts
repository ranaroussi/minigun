import { Env, mailgunApiBase } from '../env';

export type Message = {
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

function sendingDomain(env: Env, from: string): string {
  if (env.MAILGUN_DOMAIN) return env.MAILGUN_DOMAIN;
  const match = from.match(/<?([^\s<>@]+@([^\s<>]+))>?\s*$/);
  if (!match || !match[2]) {
    throw new Error(
      `MAILGUN_DOMAIN is unset and cannot derive sending domain from From header "${from}"`,
    );
  }
  return match[2];
}

export async function sendMessage(env: Env, m: Message): Promise<SendResponse> {
  const form = new FormData();
  form.append('from', m.from);
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

  const endpoint = `${mailgunApiBase(env)}/v3/${sendingDomain(env, m.from)}/messages`;
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

export async function metrics(
  env: Env,
  start: Date,
  end: Date,
  metricsList: string[],
  tag?: string,
): Promise<MetricsResponse> {
  const body: Record<string, unknown> = {
    start: start.toISOString(),
    end: end.toISOString(),
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
