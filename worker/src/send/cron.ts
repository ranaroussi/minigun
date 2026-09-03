import { Env, selfCall } from '../env';
import {
  SAFE_BATCH_FLOOR,
  listDueScheduledSends,
  listStuckSends,
  reclaimStuckBatches,
  reduceStuckSendBatchSize,
} from '../store/sends';

const STALE_MS = 2 * 60 * 1000;

export async function sweepStuckSends(env: Env): Promise<void> {
  const staleBefore = new Date(Date.now() - STALE_MS).toISOString();
  // Free any send wedged by an orphaned in_flight batch before listing:
  // listStuckSends skips sends that still have one, so this must run first.
  try {
    const reclaimed = await reclaimStuckBatches(env.DB, staleBefore);
    if (reclaimed > 0) console.warn('cron reclaimed orphaned batches', reclaimed);
  } catch (err) {
    console.error('cron reclaim stuck batches', err);
  }
  let stuck;
  try {
    stuck = await listStuckSends(env.DB, staleBefore);
  } catch (err) {
    console.error('cron list stuck sends', err);
    return;
  }
  for (const snd of stuck) {
    try {
      // A stalled send with no in_flight batch means its step chain died
      // building the batch (too large for the CPU limit). Halving toward the
      // safe floor lets a fresh kick complete; oversized sends self-heal
      // instead of needing a manual batch_size fix.
      if (snd.batch_size > SAFE_BATCH_FLOOR) {
        const reduced = await reduceStuckSendBatchSize(env.DB, snd.id, SAFE_BATCH_FLOOR);
        if (reduced > 0) console.warn('cron reduced stuck send batch_size', snd.id);
      }
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
