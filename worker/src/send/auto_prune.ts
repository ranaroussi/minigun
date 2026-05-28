import { autoPruneEnabled, autoPruneThresholds, Env } from '../env';
import { pruneCriteriaHasAny, pruneList } from '../store/hygiene';

// runAutoPruneOnce runs the configured prune thresholds against every
// list in the database. Called by the worker's scheduled handler when
// LIST_HYGIENE_AUTO_PRUNE_ENABLED is "true".
//
// Design choices:
//   - One shared threshold set across all lists. Per-list customization
//     belongs in the manual surface; the cron is for "I've audited the
//     defaults and trust them everywhere."
//   - Bounded per-call (limit=1000 per list). Massive backlogs drain over
//     multiple ticks — gives operators time to spot anomalies in audit logs.
//   - Mirrors the Go side (src/internal/worker/hygiene.go) — same
//     thresholds yield the same behavior across runtimes.
export async function runAutoPruneOnce(env: Env): Promise<void> {
  if (!autoPruneEnabled(env)) return;
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
