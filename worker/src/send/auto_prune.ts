import { autoPruneEnabled, autoPruneThresholds, Env } from '../env';
import { pruneCriteriaHasAny, pruneList } from '../store/hygiene';
import { getStateInt, setStateInt } from '../store/worker_state';

// Minimum gap between two auto-prune runs (Phase 5). The wrangler.toml
// cron fires every minute; without a throttle, an enabled auto-prune
// would re-scan every list every minute. Daily cadence matches the Go
// scheduler (src/internal/worker/hygiene.go), which uses time.NewTicker.
const AUTO_PRUNE_MIN_INTERVAL_MS = 24 * 60 * 60 * 1000;
const AUTO_PRUNE_STATE_KEY = 'auto_prune_last_run_ms';

// runAutoPruneOnce runs the configured prune thresholds against every
// list in the database. Called by the worker's scheduled handler when
// LIST_HYGIENE_AUTO_PRUNE_ENABLED is "true".
//
// Phase 5: persistent throttle via worker_state — skips silently if the
// last successful run was within AUTO_PRUNE_MIN_INTERVAL_MS. This is the
// fix for the M7 parity gap where Worker fired per cron tick (~1 min)
// while Go fired daily.
//
// Design choices:
//   - One shared threshold set across all lists. Per-list customization
//     belongs in the manual surface; the cron is for "I've audited the
//     defaults and trust them everywhere."
//   - Bounded per-call (limit=1000 per list). Massive backlogs drain over
//     multiple ticks — gives operators time to spot anomalies in audit logs.
//   - Throttle is keyed off the start of a run, not the end. If a run is
//     interrupted mid-loop we'd rather skip the next 24h than re-fire
//     immediately and double the audit-row writes.
export async function runAutoPruneOnce(env: Env): Promise<void> {
  if (!autoPruneEnabled(env)) return;
  const now = Date.now();
  const lastRun = await getStateInt(env.DB, AUTO_PRUNE_STATE_KEY);
  if (lastRun !== null && now - lastRun < AUTO_PRUNE_MIN_INTERVAL_MS) {
    return;
  }
  const t = autoPruneThresholds(env);
  const dayMs = 24 * 60 * 60 * 1000;
  const criteria = {
    minMessagesSinceEngagement: t.minMessagesSinceEngagement,
    dormantForMs: t.byRecencyDays * dayMs,
    noDeliveryForMs: t.noDeliveryDays * dayMs,
  };
  if (!pruneCriteriaHasAny(criteria)) {
    console.log('auto-prune: no thresholds set, skipping');
    return;
  }
  // Persist the run timestamp BEFORE iterating lists. A crash midway
  // means we skip the next cycle (safer than re-running and re-auditing).
  await setStateInt(env.DB, AUTO_PRUNE_STATE_KEY, now);
  const { results } = await env.DB.prepare(`SELECT id, slug FROM lists`).all<{
    id: string;
    slug: string;
  }>();
  for (const list of results ?? []) {
    try {
      const res = await pruneList(
        env.DB,
        { listID: list.id, criteria, limit: 1000 },
        false,
        0,
      );
      if (res.unsubscribed > 0) {
        console.log('auto-prune: unsubscribed', {
          list_id: list.id,
          list_slug: list.slug,
          unsubscribed: res.unsubscribed,
          candidates: res.candidates,
        });
      }
    } catch (err) {
      console.error('auto-prune: list', list.id, err);
    }
  }
}
