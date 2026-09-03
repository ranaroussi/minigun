import { describe, expect, it } from 'vitest';
import {
  MAX_BATCH_SIZE,
  MAX_THROTTLE_MS,
  SAFE_BATCH_FLOOR,
  reduceStuckSendBatchSize,
} from '../src/store/sends';

// Captures the prepared SQL and bound args and reports a `changes` count.
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

describe('batch_size guards', () => {
  it('caps the creation size at 200, keeps a safe floor of 100, throttle ceiling 500', () => {
    expect(MAX_BATCH_SIZE).toBe(200);
    expect(SAFE_BATCH_FLOOR).toBe(100);
    expect(MAX_THROTTLE_MS).toBe(500);
    expect(SAFE_BATCH_FLOOR).toBeLessThanOrEqual(MAX_BATCH_SIZE);
  });

  it('halves toward the floor and only touches oversized running sends', async () => {
    const { db, captured } = dbCapturing(1);

    const n = await reduceStuckSendBatchSize(db, 's_1', SAFE_BATCH_FLOOR);

    expect(n).toBe(1);
    expect(captured.sql).toContain('batch_size = MAX(?, batch_size / 2)');
    expect(captured.sql).toContain('batch_size > ?');
    expect(captured.sql).toContain("status IN ('queued', 'running')");
    // floor bound first and last (SET floor + WHERE guard), send id in between.
    expect(captured.args[0]).toBe(SAFE_BATCH_FLOOR);
    expect(captured.args[captured.args.length - 1]).toBe(SAFE_BATCH_FLOOR);
    expect(captured.args).toContain('s_1');
  });

  it('reports 0 when the send is already at/under the floor', async () => {
    const { db } = dbCapturing(0);
    expect(await reduceStuckSendBatchSize(db, 's_2', SAFE_BATCH_FLOOR)).toBe(0);
  });
});
