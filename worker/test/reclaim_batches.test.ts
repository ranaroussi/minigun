import { describe, expect, it } from 'vitest';
import { reclaimStuckBatches } from '../src/store/sends';

// Captures the prepared SQL and bound args, and reports a caller-supplied
// `changes` count so the reclaim return value is observable.
function dbCapturing(changes: number) {
  const captured: { sql: string; args: unknown[] } = { sql: '', args: [] };
  const prepared: any = {
    bind: (...args: unknown[]) => {
      captured.args = args;
      return prepared;
    },
    run: async () => ({ meta: { changes } }),
  };
  const db: any = {
    prepare: (sql: string) => {
      captured.sql = sql;
      return prepared;
    },
  };
  return { db: db as D1Database, captured };
}

describe('reclaimStuckBatches', () => {
  it('flips only stale in_flight batches to failed and returns the count', async () => {
    const { db, captured } = dbCapturing(2);
    const staleBefore = '2026-07-09T11:00:00.000Z';

    const n = await reclaimStuckBatches(db, staleBefore);

    expect(n).toBe(2);
    expect(captured.sql).toContain("status = 'failed'");
    expect(captured.sql).toContain("status = 'in_flight'");
    expect(captured.sql).toContain('updated_at < ?');
    // Last bound arg is the stale cutoff; the first is the reclaim timestamp.
    expect(captured.args[captured.args.length - 1]).toBe(staleBefore);
  });

  it('returns 0 when nothing is orphaned', async () => {
    const { db } = dbCapturing(0);
    expect(await reclaimStuckBatches(db, '2026-07-09T11:00:00.000Z')).toBe(0);
  });
});
