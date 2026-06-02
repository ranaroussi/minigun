import { Env, selfCall } from '../env';
import { listDueScheduledSends, listStuckSends } from '../store/sends';

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
      const resp = await selfCall(env, `/send/${snd.id}/next`);
      if (!resp.ok) console.error('cron kick non-ok', snd.id, resp.status);
    } catch (err) {
      console.error('cron kick', snd.id, err);
    }
  }
}

// dispatchDueSends picks up future-dated sends whose send_at has arrived and
// kicks them through the normal step path (POST /send/:id/next), which flips
// them from 'scheduled' to 'running'. Scheduling granularity is bounded by
// the cron tick — fine for email.
export async function dispatchDueSends(env: Env): Promise<void> {
  let due;
  try {
    due = await listDueScheduledSends(env.DB);
  } catch (err) {
    console.error('cron list due scheduled sends', err);
    return;
  }
  for (const snd of due) {
    try {
      const resp = await selfCall(env, `/send/${snd.id}/next`);
      if (!resp.ok) console.error('cron dispatch non-ok', snd.id, resp.status);
    } catch (err) {
      console.error('cron dispatch scheduled', snd.id, err);
    }
  }
}
