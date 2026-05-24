import { Env } from '../env';
import { perSendMetrics } from '../lib/mailgun';
import { applyMailgunStats, listDueSendStats, recordStatsFetchError } from '../store/stats';

const STATS_POLL_OFFSETS_MS = [
  0,
  1 * 60 * 60 * 1000,
  6 * 60 * 60 * 1000,
  24 * 60 * 60 * 1000,
  48 * 60 * 60 * 1000,
  120 * 60 * 60 * 1000,
];

export function nextStatsFetch(
  completedAt: Date,
  now: Date,
): { next: Date | null; isFinal: boolean } {
  const elapsed = now.getTime() - completedAt.getTime();
  for (const off of STATS_POLL_OFFSETS_MS) {
    if (off > elapsed) {
      return { next: new Date(completedAt.getTime() + off), isFinal: false };
    }
  }
  return { next: null, isFinal: true };
}

export async function refreshDueStats(env: Env, limit = 25): Promise<void> {
  let due;
  try {
    due = await listDueSendStats(env.DB, limit);
  } catch (err) {
    console.error('stats: list due', err);
    return;
  }
  for (const row of due) {
    try {
      await refreshOneSendStats(env, row.send_id, row.created_at, row.completed_at);
    } catch (err) {
      console.error('stats: refresh', row.send_id, err);
    }
  }
}

async function refreshOneSendStats(
  env: Env,
  sendID: string,
  sendCreatedAtISO: string,
  completedAtISO: string,
): Promise<void> {
  const completedAt = new Date(completedAtISO);
  const now = new Date();
  const { next, isFinal } = nextStatsFetch(completedAt, now);

  try {
    const totals = await perSendMetrics(env, sendID, new Date(sendCreatedAtISO));
    await applyMailgunStats(env.DB, sendID, {
      sent: totals.sent,
      delivered: totals.delivered,
      opened: totals.opened,
      clicked: totals.clicked,
      failed: totals.failed,
      complained: totals.complained,
      next_fetch_at: next?.toISOString() ?? null,
      is_final: isFinal,
      fetch_error: null,
    });
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err);
    await recordStatsFetchError(env.DB, sendID, next?.toISOString() ?? null, isFinal, msg);
  }
}
