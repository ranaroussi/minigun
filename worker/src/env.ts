export type Env = {
  DB: D1Database;

  // Service binding to this same Worker. Used to drive the batch-send chain
  // (scheduleNextStep + cron sweeps) by invoking the Worker directly instead
  // of fetch()-ing its own public hostname — a request to a Worker's own
  // route is not reliably routed back into the Worker (the "loopback"
  // problem), which silently stalled every multi-batch send.
  SELF: Fetcher;

  MINIGUN_PUBLIC_URL: string;
  MAILGUN_REGION?: string;
  MAILGUN_API_BASE?: string;

  MAILGUN_API_KEY: string;
  REDIRECT_URL?: string;
  MINIGUN_HMAC_SECRET: string;
  MINIGUN_INTERNAL_SECRET: string;
  MINIGUN_API_TOKEN?: string;
  MINIGUN_TURNSTILE_SITE_KEY?: string;
  MINIGUN_TURNSTILE_SECRET_KEY?: string;
  MAILGUN_WEBHOOK_SIGNING_KEY?: string;

  // Feature flag for engagement-stats retrieval: the events-pull cron that
  // fetches Mailgun events into the per-recipient engagement rollups
  // (contact_message_engagement / contact_message_clicks / contact_engagement).
  // When undefined or anything other than "true", the pull stays dormant.
  // This gates RETRIEVAL only; acting on the data (pruning) is the separate
  // LIST_HYGIENE_AUTO_PRUNE_ENABLED flag.
  ENGAGEMENT_STATS_ENABLED?: string;
  // Deprecated alias for ENGAGEMENT_STATS_ENABLED, kept for backward compat.
  EVENTS_ARCHIVE_ENABLED?: string;

  // Feature flag for the auto-prune cron (Phase 4). When "true", every
  // scheduled tick runs the prune executor against every list with the
  // configured thresholds. Default off — see README "Automatic list
  // hygiene" for the safety contract.
  LIST_HYGIENE_AUTO_PRUNE_ENABLED?: string;
  // Conservative defaults: 20 wasted deliveries OR 180 days no engagement.
  LIST_HYGIENE_AUTO_PRUNE_BY_COUNT?: string;
  LIST_HYGIENE_AUTO_PRUNE_BY_RECENCY_DAYS?: string;
  LIST_HYGIENE_AUTO_PRUNE_NO_DELIVERY_DAYS?: string;
};

export function engagementStatsEnabled(env: Env): boolean {
  // Prefer the current name; fall back to the deprecated EVENTS_ARCHIVE_ENABLED.
  const v = env.ENGAGEMENT_STATS_ENABLED ?? env.EVENTS_ARCHIVE_ENABLED ?? '';
  return v.toLowerCase() === 'true';
}

export function autoPruneEnabled(env: Env): boolean {
  return (env.LIST_HYGIENE_AUTO_PRUNE_ENABLED ?? '').toLowerCase() === 'true';
}

export function autoPruneThresholds(env: Env): {
  minMessagesSinceEngagement: number;
  byRecencyDays: number;
  noDeliveryDays: number;
} {
  const parse = (s: string | undefined, def: number): number => {
    const n = parseInt(s ?? '', 10);
    return Number.isFinite(n) && n >= 0 ? n : def;
  };
  return {
    minMessagesSinceEngagement: parse(env.LIST_HYGIENE_AUTO_PRUNE_BY_COUNT, 20),
    byRecencyDays: parse(env.LIST_HYGIENE_AUTO_PRUNE_BY_RECENCY_DAYS, 180),
    noDeliveryDays: parse(env.LIST_HYGIENE_AUTO_PRUNE_NO_DELIVERY_DAYS, 0),
  };
}

export function mailgunApiBase(env: Env): string {
  if (env.MAILGUN_API_BASE) return env.MAILGUN_API_BASE.replace(/\/$/, '');
  const region = (env.MAILGUN_REGION ?? 'us').toLowerCase();
  return region === 'eu' ? 'https://api.eu.mailgun.net' : 'https://api.mailgun.net';
}

export function publicURL(env: Env): string {
  return (env.MINIGUN_PUBLIC_URL ?? '').replace(/\/$/, '');
}

// selfCall invokes one of this Worker's own internal endpoints (e.g.
// /send/:id/next) through the SELF service binding. This re-enters the
// Worker directly, avoiding the same-Worker loopback that drops a request
// made to the Worker's own public route. The host in the URL is irrelevant
// for a service binding (it routes by binding, not DNS), so we use a fixed
// internal host. The x-internal-secret header satisfies bearerAuth's
// internal-path bypass.
export function selfCall(env: Env, path: string): Promise<Response> {
  return env.SELF.fetch(
    new Request(`https://minigun.internal${path}`, {
      method: 'POST',
      headers: { 'x-internal-secret': env.MINIGUN_INTERNAL_SECRET },
    }),
  );
}
