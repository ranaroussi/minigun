import { sign as signToken } from '../lib/token';
import { Env, publicURL } from '../env';
import { Recipient, Send } from '../store/types';

export async function buildRecipientVars(
  env: Env,
  send: Send,
  r: Recipient,
): Promise<Record<string, unknown>> {
  const out: Record<string, unknown> = {};
  if (r.params) {
    try {
      const parsed = JSON.parse(r.params) as Record<string, unknown>;
      for (const [k, v] of Object.entries(parsed)) out[k] = v;
    } catch {}
  }
  const tok = await signToken(env.MINIGUN_HMAC_SECRET, send.id, r.subscription_id);
  const base = publicURL(env);
  const unsubURL = `${base}/u/${tok}`;
  out.unsub_url = unsubURL;
  out.unsubscribe = unsubURL;
  out.manage_url = `${base}/manage/${tok}`;
  return out;
}
