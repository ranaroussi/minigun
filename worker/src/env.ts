export type Env = {
  DB: D1Database;

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
};

export function mailgunApiBase(env: Env): string {
  if (env.MAILGUN_API_BASE) return env.MAILGUN_API_BASE.replace(/\/$/, '');
  const region = (env.MAILGUN_REGION ?? 'us').toLowerCase();
  return region === 'eu' ? 'https://api.eu.mailgun.net' : 'https://api.mailgun.net';
}

export function publicURL(env: Env): string {
  return (env.MINIGUN_PUBLIC_URL ?? '').replace(/\/$/, '');
}
