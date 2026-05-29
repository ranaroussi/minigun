import { unsubscribeAndAudit } from './unsubs';
import { NotFoundError } from './types';

// ---------------------------------------------------------------------------
// List hygiene — prune candidates query + executor
// ---------------------------------------------------------------------------

// PruneCriteria narrows what counts as prune-eligible. The three signals
// are independent — a contact matches when ANY enabled criterion holds.
// Zero/undefined disables the corresponding criterion (so an empty
// criteria set matches nothing — the safe default).
//
// See src/internal/store/hygiene.go for full semantics; this is a 1:1
// mirror of the Go side so both runtimes return identical candidate sets.
export type PruneCriteria = {
  minMessagesSinceEngagement?: number;
  dormantForMs?: number;
  noDeliveryForMs?: number;
};

export function pruneCriteriaHasAny(c: PruneCriteria): boolean {
  return (
    (c.minMessagesSinceEngagement ?? 0) > 0 ||
    (c.dormantForMs ?? 0) > 0 ||
    (c.noDeliveryForMs ?? 0) > 0
  );
}

export type PruneReason =
  | 'auto-prune-by-count'
  | 'auto-prune-by-recency'
  | 'auto-prune-by-no-delivery'
  | 'auto-prune';

export type PruneCandidate = {
  subscription_id: number;
  contact_id: string;
  email: string;
  messages_since_last_engagement: number;
  last_engagement_at_ms: number | null;
  last_delivered_at_ms: number | null;
  total_delivered: number;
  matched_by_count: boolean;
  matched_by_recency: boolean;
  matched_by_no_delivery: boolean;
};

export function candidateReason(c: PruneCandidate): PruneReason {
  if (c.matched_by_count) return 'auto-prune-by-count';
  if (c.matched_by_recency) return 'auto-prune-by-recency';
  if (c.matched_by_no_delivery) return 'auto-prune-by-no-delivery';
  return 'auto-prune';
}

export type ListPruneCandidatesParams = {
  listID: string;
  criteria: PruneCriteria;
  nowMs?: number;
  limit?: number;
};

export async function listPruneCandidates(
  db: D1Database,
  p: ListPruneCandidatesParams,
): Promise<PruneCandidate[]> {
  if (!p.listID) throw new Error('listPruneCandidates: listID is required');
  if (!pruneCriteriaHasAny(p.criteria)) {
    throw new Error('listPruneCandidates: at least one criterion is required');
  }
  const nowMs = p.nowMs ?? Date.now();
  let limit = p.limit ?? 1000;
  if (limit <= 0) limit = 1000;
  if (limit > 10000) limit = 10000;

  // Build SELECT flag expressions + WHERE predicates in lockstep so the
  // bind-order is obvious. SELECT args come BEFORE WHERE args.
  const selectFlags: { sql: string; args: unknown[] }[] = [];
  const wherePreds: { sql: string; args: unknown[] }[] = [];

  // Each flag expression wraps the comparison in a CASE/COALESCE so the
  // LEFT JOIN's NULL rows produce a clean 0/1 — without this, comparisons
  // against NULL leak NULL into the SELECT and break the int scan path.
  if ((p.criteria.minMessagesSinceEngagement ?? 0) > 0) {
    const n = p.criteria.minMessagesSinceEngagement!;
    selectFlags.push({
      sql: '(COALESCE(ce.messages_since_last_engagement, 0) >= ?) AS matched_by_count',
      args: [n],
    });
    wherePreds.push({
      sql: '(COALESCE(ce.messages_since_last_engagement, 0) >= ?)',
      args: [n],
    });
  } else {
    selectFlags.push({ sql: '0 AS matched_by_count', args: [] });
  }

  if ((p.criteria.dormantForMs ?? 0) > 0) {
    const cutoff = nowMs - p.criteria.dormantForMs!;
    selectFlags.push({
      sql: '(CASE WHEN ce.last_engagement_at_ms IS NOT NULL AND ce.last_engagement_at_ms < ? THEN 1 ELSE 0 END) AS matched_by_recency',
      args: [cutoff],
    });
    wherePreds.push({
      sql: '(ce.last_engagement_at_ms IS NOT NULL AND ce.last_engagement_at_ms < ?)',
      args: [cutoff],
    });
  } else {
    selectFlags.push({ sql: '0 AS matched_by_recency', args: [] });
  }

  if ((p.criteria.noDeliveryForMs ?? 0) > 0) {
    const cutoff = nowMs - p.criteria.noDeliveryForMs!;
    selectFlags.push({
      sql: "(CASE WHEN CAST(strftime('%s', subs.subscribed_at) AS INTEGER) * 1000 < ? AND (ce.contact_id IS NULL OR ce.last_delivered_at_ms IS NULL OR ce.last_delivered_at_ms < ?) THEN 1 ELSE 0 END) AS matched_by_no_delivery",
      args: [cutoff, cutoff],
    });
    wherePreds.push({
      sql: "(CAST(strftime('%s', subs.subscribed_at) AS INTEGER) * 1000 < ? AND (ce.contact_id IS NULL OR ce.last_delivered_at_ms IS NULL OR ce.last_delivered_at_ms < ?))",
      args: [cutoff, cutoff],
    });
  } else {
    selectFlags.push({ sql: '0 AS matched_by_no_delivery', args: [] });
  }

  const sql = `
    SELECT
      subs.id, subs.contact_id, c.email,
      COALESCE(ce.messages_since_last_engagement, 0) AS messages_since_last_engagement,
      ce.last_engagement_at_ms,
      ce.last_delivered_at_ms,
      COALESCE(ce.total_delivered, 0) AS total_delivered,
      ${selectFlags.map((f) => f.sql).join(',\n      ')}
    FROM subscriptions subs
    JOIN contacts c ON c.id = subs.contact_id
    LEFT JOIN contact_engagement ce
           ON ce.contact_id = subs.contact_id AND ce.list_id = subs.list_id
    WHERE subs.list_id = ?
      AND subs.subscribed = 1
      AND (${wherePreds.map((p) => p.sql).join(' OR ')})
    ORDER BY ce.messages_since_last_engagement DESC, subs.id ASC
    LIMIT ?`;

  const args: unknown[] = [];
  for (const f of selectFlags) args.push(...f.args);
  args.push(p.listID);
  for (const w of wherePreds) args.push(...w.args);
  args.push(limit);

  const { results } = await db.prepare(sql).bind(...args).all<{
    id: number;
    contact_id: string;
    email: string;
    messages_since_last_engagement: number;
    last_engagement_at_ms: number | null;
    last_delivered_at_ms: number | null;
    total_delivered: number;
    matched_by_count: number;
    matched_by_recency: number;
    matched_by_no_delivery: number;
  }>();

  return (results ?? []).map((r) => ({
    subscription_id: r.id,
    contact_id: r.contact_id,
    email: r.email,
    messages_since_last_engagement: r.messages_since_last_engagement ?? 0,
    last_engagement_at_ms: r.last_engagement_at_ms,
    last_delivered_at_ms: r.last_delivered_at_ms,
    total_delivered: r.total_delivered ?? 0,
    matched_by_count: r.matched_by_count === 1,
    matched_by_recency: r.matched_by_recency === 1,
    matched_by_no_delivery: r.matched_by_no_delivery === 1,
  }));
}

export type PruneListResult = {
  list_id: string;
  dry_run: boolean;
  candidates: number;
  unsubscribed: number;
  sample: PruneCandidate[];
  reason_counts: Record<string, number>;
};

export async function pruneList(
  db: D1Database,
  p: ListPruneCandidatesParams,
  dryRun: boolean,
  sampleSize = 25,
): Promise<PruneListResult> {
  const candidates = await listPruneCandidates(db, p);
  if (sampleSize <= 0) sampleSize = 25;
  if (sampleSize > candidates.length) sampleSize = candidates.length;

  const reasonCounts: Record<string, number> = {};
  for (const c of candidates) {
    const r = candidateReason(c);
    reasonCounts[r] = (reasonCounts[r] ?? 0) + 1;
  }
  const result: PruneListResult = {
    list_id: p.listID,
    dry_run: dryRun,
    candidates: candidates.length,
    unsubscribed: 0,
    sample: candidates.slice(0, sampleSize),
    reason_counts: reasonCounts,
  };
  if (dryRun) return result;
  // Apply: atomically unsubscribe + audit each candidate via a single
  // D1 batch. Phase 5 fix for H4 — Phase 4 used recordUnsubscribeEvent
  // after a separate unsubscribe call, which left a window for orphaned
  // unsubscribes if the audit insert failed.
  for (const c of candidates) {
    try {
      await unsubscribeAndAudit(db, p.listID, c.contact_id, c.email, candidateReason(c));
    } catch (err) {
      if (err instanceof NotFoundError) continue;
      throw err;
    }
    result.unsubscribed++;
  }
  return result;
}
