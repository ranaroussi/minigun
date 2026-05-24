export type Unsubscribe = {
  sendID: string;
  subscriptionID: number;
};

export class InvalidTokenError extends Error {
  constructor() {
    super('invalid unsubscribe token');
    this.name = 'InvalidTokenError';
  }
}

async function hmacKey(secret: string): Promise<CryptoKey> {
  return crypto.subtle.importKey(
    'raw',
    new TextEncoder().encode(secret),
    { name: 'HMAC', hash: 'SHA-256' },
    false,
    ['sign'],
  );
}

async function computeMAC(secret: string, sendID: string, subID: number): Promise<Uint8Array> {
  const key = await hmacKey(secret);
  const msg = new TextEncoder().encode(`${sendID}:${subID}`);
  const sig = await crypto.subtle.sign('HMAC', key, msg);
  return new Uint8Array(sig).slice(0, 16);
}

function encodeB64Url(bytes: Uint8Array): string {
  let bin = '';
  for (const b of bytes) bin += String.fromCharCode(b);
  return btoa(bin).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

function decodeB64Url(s: string): Uint8Array {
  let b64 = s.replace(/-/g, '+').replace(/_/g, '/');
  while (b64.length % 4) b64 += '=';
  const bin = atob(b64);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

function constantTimeEqual(a: Uint8Array, b: Uint8Array): boolean {
  if (a.length !== b.length) return false;
  let diff = 0;
  for (let i = 0; i < a.length; i++) diff |= a[i]! ^ b[i]!;
  return diff === 0;
}

export async function sign(secret: string, sendID: string, subscriptionID: number): Promise<string> {
  const mac = await computeMAC(secret, sendID, subscriptionID);
  return `${sendID}.${subscriptionID}.${encodeB64Url(mac)}`;
}

export async function verify(secret: string, token: string): Promise<Unsubscribe> {
  const parts = token.split('.');
  if (parts.length !== 3) throw new InvalidTokenError();
  const [sendID, subStr, macStr] = parts as [string, string, string];
  if (!/^-?\d+$/.test(subStr)) throw new InvalidTokenError();
  const subID = Number(subStr);
  if (!Number.isSafeInteger(subID)) throw new InvalidTokenError();
  let provided: Uint8Array;
  try {
    provided = decodeB64Url(macStr);
  } catch {
    throw new InvalidTokenError();
  }
  const expected = await computeMAC(secret, sendID, subID);
  if (!constantTimeEqual(expected, provided)) throw new InvalidTokenError();
  return { sendID, subscriptionID: subID };
}
