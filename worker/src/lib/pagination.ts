export const DEFAULT_PAGE_LIMIT = 50;
export const MAX_PAGE_LIMIT = 500;

export type Cursor = {
  afterIntID?: number;
  afterStringID?: string;
  afterCreated?: string;
};

function encodeB64UrlString(s: string): string {
  return btoa(s).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

function decodeB64UrlString(s: string): string {
  let b64 = s.replace(/-/g, '+').replace(/_/g, '/');
  while (b64.length % 4) b64 += '=';
  return atob(b64);
}

export function encodeCursor(c: Cursor): string {
  const obj: Record<string, unknown> = {};
  if (c.afterIntID !== undefined && c.afterIntID !== 0) obj['i'] = c.afterIntID;
  if (c.afterStringID) obj['s'] = c.afterStringID;
  if (c.afterCreated) obj['t'] = c.afterCreated;
  return encodeB64UrlString(JSON.stringify(obj));
}

export function decodeCursor(s: string | null | undefined): Cursor {
  if (!s) return {};
  let raw: string;
  try {
    raw = decodeB64UrlString(s);
  } catch {
    throw new Error('invalid cursor');
  }
  let obj: Record<string, unknown>;
  try {
    obj = JSON.parse(raw);
  } catch {
    throw new Error('invalid cursor');
  }
  const c: Cursor = {};
  if (typeof obj['i'] === 'number') c.afterIntID = obj['i'];
  if (typeof obj['s'] === 'string') c.afterStringID = obj['s'];
  if (typeof obj['t'] === 'string') c.afterCreated = obj['t'];
  return c;
}

export function clampLimit(n: number | null | undefined): number {
  if (!n || n <= 0) return DEFAULT_PAGE_LIMIT;
  if (n > MAX_PAGE_LIMIT) return MAX_PAGE_LIMIT;
  return Math.floor(n);
}
