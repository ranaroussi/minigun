// Mailgun signs webhook deliveries with HMAC-SHA256(timestamp + token)
// using the per-account "HTTP webhook signing key" (Sending → Webhooks
// in the dashboard). We mirror that scheme exactly:
//   expected = hex(HMAC_SHA256(signingKey, timestamp + token))
//   accept   = constant-time-equal(expected, signature)
//
// We additionally reject signatures whose `timestamp` is more than
// FRESHNESS_SECONDS old to bound replay risk. Mailgun retries failed
// deliveries within an 8h window, so 15min is comfortably wider than
// any legitimate jitter but tight enough that a leaked signature
// can't be replayed days later.

export type MailgunSignature = {
  timestamp: string;
  token: string;
  signature: string;
};

export const FRESHNESS_SECONDS = 15 * 60;

const HEX_RE = /^[0-9a-f]+$/i;

export async function verifyMailgunSignature(
  signingKey: string,
  sig: MailgunSignature,
  nowSec: number = Math.floor(Date.now() / 1000),
): Promise<{ ok: true } | { ok: false; reason: string }> {
  if (!signingKey) return { ok: false, reason: 'no signing key configured' };
  if (!sig.timestamp || !sig.token || !sig.signature) {
    return { ok: false, reason: 'missing timestamp / token / signature' };
  }
  if (!HEX_RE.test(sig.signature)) return { ok: false, reason: 'signature not hex' };

  const ts = Number(sig.timestamp);
  if (!Number.isFinite(ts)) return { ok: false, reason: 'timestamp not numeric' };
  if (Math.abs(nowSec - ts) > FRESHNESS_SECONDS) {
    return { ok: false, reason: 'timestamp stale or in the future' };
  }

  const key = await crypto.subtle.importKey(
    'raw',
    new TextEncoder().encode(signingKey),
    { name: 'HMAC', hash: 'SHA-256' },
    false,
    ['sign'],
  );
  const macBuf = await crypto.subtle.sign(
    'HMAC',
    key,
    new TextEncoder().encode(sig.timestamp + sig.token),
  );
  const expected = bufToHex(macBuf);

  return constantTimeEqual(expected, sig.signature.toLowerCase())
    ? { ok: true }
    : { ok: false, reason: 'signature mismatch' };
}

function bufToHex(buf: ArrayBuffer): string {
  const b = new Uint8Array(buf);
  let out = '';
  for (let i = 0; i < b.length; i++) {
    const byte = b[i] ?? 0;
    out += byte.toString(16).padStart(2, '0');
  }
  return out;
}

function constantTimeEqual(a: string, b: string): boolean {
  if (a.length !== b.length) return false;
  let diff = 0;
  for (let i = 0; i < a.length; i++) diff |= a.charCodeAt(i) ^ b.charCodeAt(i);
  return diff === 0;
}
