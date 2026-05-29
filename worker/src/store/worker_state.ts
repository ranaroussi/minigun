// ---------------------------------------------------------------------------
// worker_state — tiny key/value sentinel store
// ---------------------------------------------------------------------------
//
// Persistent state for cron throttles ("run at most once per day"-style
// invariants that need to survive Worker invocation boundaries — Workers
// are stateless per invocation, so any inter-tick memory needs to live in
// D1). Mirrors src/internal/store/worker_state.go.
//
// Well-known keys (kept in sync with the Go side):
//   auto_prune_last_run_ms — epoch-ms of the last successful auto-prune.

import { nowISO } from './types';

export async function getState(
  db: D1Database,
  key: string,
): Promise<string | null> {
  const row = await db
    .prepare('SELECT value FROM worker_state WHERE key = ?')
    .bind(key)
    .first<{ value: string }>();
  return row?.value ?? null;
}

export async function setState(
  db: D1Database,
  key: string,
  value: string,
): Promise<void> {
  await db
    .prepare(
      `INSERT INTO worker_state (key, value, updated_at)
       VALUES (?, ?, ?)
       ON CONFLICT(key) DO UPDATE SET
         value      = excluded.value,
         updated_at = excluded.updated_at`,
    )
    .bind(key, value, nowISO())
    .run();
}

// Convenience: parse the value as a base-10 integer. Returns null for
// missing keys and for unparseable values (callers treat both as "no
// throttle in effect").
export async function getStateInt(
  db: D1Database,
  key: string,
): Promise<number | null> {
  const v = await getState(db, key);
  if (v === null) return null;
  const n = parseInt(v, 10);
  return Number.isFinite(n) ? n : null;
}

export async function setStateInt(
  db: D1Database,
  key: string,
  n: number,
): Promise<void> {
  await setState(db, key, String(n));
}
