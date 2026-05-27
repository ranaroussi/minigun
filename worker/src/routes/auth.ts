import { MiddlewareHandler } from 'hono';
import { Env } from '../env';

function constantTimeEqual(a: string, b: string): boolean {
  if (a.length !== b.length) return false;
  let diff = 0;
  for (let i = 0; i < a.length; i++) diff |= a.charCodeAt(i) ^ b.charCodeAt(i);
  return diff === 0;
}

function isExempt(pathname: string): boolean {
  if (pathname === '/') return true;
  if (pathname === '/healthz') return true;
  if (pathname.startsWith('/u/')) return true;
  if (pathname.startsWith('/manage/')) return true;
  // Webhooks authenticate via Mailgun HMAC signature instead of the
  // operator's Bearer token (the third-party caller doesn't have one).
  if (pathname.startsWith('/webhooks/')) return true;
  return false;
}

const INTERNAL_PATH_RE = /^\/send\/[^/]+\/(next|resume)$/;

function checkInternalSecret(c: Parameters<MiddlewareHandler<{ Bindings: Env }>>[0]): boolean {
  const expected = c.env.MINIGUN_INTERNAL_SECRET;
  if (!expected) return false;
  const provided = c.req.header('x-internal-secret') ?? '';
  if (!provided) return false;
  return constantTimeEqual(provided, expected);
}

export const bearerAuth: MiddlewareHandler<{ Bindings: Env }> = async (c, next) => {
  const token = c.env.MINIGUN_API_TOKEN;
  if (!token) return next();
  const pathname = new URL(c.req.url).pathname;
  if (isExempt(pathname)) return next();
  if (INTERNAL_PATH_RE.test(pathname) && checkInternalSecret(c)) return next();
  const header = c.req.header('Authorization') ?? '';
  const prefix = 'Bearer ';
  if (!header.startsWith(prefix)) return c.json({ error: 'missing bearer token' }, 401);
  if (!constantTimeEqual(header.slice(prefix.length), token)) {
    return c.json({ error: 'invalid bearer token' }, 401);
  }
  return next();
};
