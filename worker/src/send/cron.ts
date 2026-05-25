import { Env, publicURL } from '../env';
import { listStuckSends } from '../store/sends';

const STALE_MS = 2 * 60 * 1000;

export async function sweepStuckSends(env: Env): Promise<void> {
  const staleBefore = new Date(Date.now() - STALE_MS).toISOString();
  let stuck;
  try {
    stuck = await listStuckSends(env.DB, staleBefore);
  } catch (err) {
    console.error('cron list stuck sends', err);
    return;
  }
  for (const snd of stuck) {
    try {
      await fetch(`${publicURL(env)}/send/${snd.id}/next`, {
        method: 'POST',
        headers: { 'x-internal-secret': env.MINIGUN_INTERNAL_SECRET },
      });
    } catch (err) {
      console.error('cron kick', snd.id, err);
    }
  }
}
