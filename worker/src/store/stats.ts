import { DueStatsRow, SendStats, nowISO } from './types';

type SendStatsRow = {
  send_id: string;
  sent: number;
  delivered: number;
  opened: number;
  clicked: number;
  failed: number;
  complained: number;
  unsubscribed: number;
  first_fetched_at: string | null;
  last_fetched_at: string | null;
  next_fetch_at: string | null;
  is_final: number;
  fetch_error: string | null;
  created_at: string;
  updated_at: string;
};

function rowToStats(r: SendStatsRow): SendStats {
  return {
    send_id: r.send_id,
    sent: r.sent ?? 0,
    delivered: r.delivered ?? 0,
    opened: r.opened ?? 0,
    clicked: r.clicked ?? 0,
    failed: r.failed ?? 0,
    complained: r.complained ?? 0,
    unsubscribed: r.unsubscribed ?? 0,
    first_fetched_at: r.first_fetched_at,
    last_fetched_at: r.last_fetched_at,
    next_fetch_at: r.next_fetch_at,
    is_final: r.is_final === 1,
    fetch_error: r.fetch_error,
    created_at: r.created_at,
    updated_at: r.updated_at,
  };
}

export async function getSendStats(db: D1Database, sendID: string): Promise<SendStats | null> {
  const row = await db
    .prepare(
      `SELECT send_id, sent, delivered, opened, clicked, failed, complained, unsubscribed,
              first_fetched_at, last_fetched_at, next_fetch_at, is_final, fetch_error,
              created_at, updated_at
         FROM send_stats WHERE send_id = ?`,
    )
    .bind(sendID)
    .first<SendStatsRow>();
  return row ? rowToStats(row) : null;
}

export function initSendStatsStmt(db: D1Database, sendID: string): D1PreparedStatement {
  return db.prepare(`INSERT OR IGNORE INTO send_stats (send_id) VALUES (?)`).bind(sendID);
}

export function markSendCompletedForStatsStmt(
  db: D1Database,
  sendID: string,
): D1PreparedStatement {
  // next_fetch_at must be written in the SAME format everywhere (JS ISO-8601,
  // e.g. "2026-06-05T12:09:15.905Z") so listDueSendStats can compare it as a
  // raw string and drive off idx_send_stats_due. Previously this used
  // datetime('now') (SQLite's " " separator), which forced a datetime()
  // wrapper in the due query, made the index non-sargable, and regressed the
  // plan to a full sends scan (~9k rows/tick).
  const now = nowISO();
  return db
    .prepare(
      `UPDATE send_stats SET next_fetch_at = ?, updated_at = ?
         WHERE send_id = ? AND next_fetch_at IS NULL AND is_final = 0`,
    )
    .bind(now, now, sendID);
}

export function incrementSendStatsUnsubscribedStmt(
  db: D1Database,
  sendID: string,
): D1PreparedStatement {
  return db
    .prepare(
      `INSERT INTO send_stats (send_id, unsubscribed, updated_at)
         VALUES (?, 1, datetime('now'))
         ON CONFLICT(send_id) DO UPDATE SET
           unsubscribed = unsubscribed + 1,
           updated_at = datetime('now')`,
    )
    .bind(sendID);
}

export type ApplyMailgunStatsParams = {
  sent: number;
  delivered: number;
  opened: number;
  clicked: number;
  failed: number;
  complained: number;
  next_fetch_at: string | null;
  is_final: boolean;
  fetch_error?: string | null;
};

export async function recordStatsFetchError(
  db: D1Database,
  sendID: string,
  nextFetchAt: string | null,
  isFinal: boolean,
  errMsg: string,
): Promise<void> {
  const now = nowISO();
  await db
    .prepare(
      `UPDATE send_stats SET
         last_fetched_at = ?,
         next_fetch_at = ?,
         is_final = ?,
         fetch_error = ?,
         updated_at = ?
       WHERE send_id = ?`,
    )
    .bind(now, isFinal ? null : nextFetchAt, isFinal ? 1 : 0, errMsg, now, sendID)
    .run();
}

export async function applyMailgunStats(
  db: D1Database,
  sendID: string,
  p: ApplyMailgunStatsParams,
): Promise<void> {
  const now = nowISO();
  await db
    .prepare(
      `UPDATE send_stats SET
         sent = ?, delivered = ?, opened = ?, clicked = ?, failed = ?, complained = ?,
         first_fetched_at = COALESCE(first_fetched_at, ?),
         last_fetched_at = ?,
         next_fetch_at = ?,
         is_final = ?,
         fetch_error = ?,
         updated_at = ?
       WHERE send_id = ?`,
    )
    .bind(
      p.sent,
      p.delivered,
      p.opened,
      p.clicked,
      p.failed,
      p.complained,
      now,
      now,
      p.is_final ? null : p.next_fetch_at,
      p.is_final ? 1 : 0,
      p.fetch_error ?? null,
      now,
      sendID,
    )
    .run();
}

export async function listDueSendStats(
  db: D1Database,
  limit: number,
): Promise<DueStatsRow[]> {
  const { results } = await db
    .prepare(
      // Shape matters more than it looks. Two constraints, both learned the
      // hard way after this query burned 27M rows/day (the entire D1 daily
      // read allowance) by full-scanning sends every tick:
      //
      //  1. next_fetch_at is written in ONE format everywhere (JS ISO-8601 via
      //     nowISO), so a raw lexical "<=" is exactly chronological and keeps
      //     idx_send_stats_due sargable. The old datetime()-wrapped compare was
      //     non-sargable. Migration 0015 backfills legacy SQLite-format rows.
      //  2. The due set is resolved in a LIMITed subquery over send_stats
      //     ALONE. SQLite cannot flatten a subquery that has LIMIT, so it must
      //     materialise <=limit rows first and can never choose sends as the
      //     driving table. Do NOT rewrite this as a flat two-table join: D1
      //     does not honour ANALYZE statistics, so a flat join is free to pick
      //     the catastrophic plan and there is no way to talk it out of it.
      `SELECT s.id AS send_id, s.created_at, s.completed_at
         FROM (
           SELECT send_id, next_fetch_at
             FROM send_stats
            WHERE is_final = 0
              AND next_fetch_at IS NOT NULL
              AND next_fetch_at <= ?
            ORDER BY next_fetch_at ASC
            LIMIT ?
         ) due
         JOIN sends s ON s.id = due.send_id
        WHERE s.completed_at IS NOT NULL
        ORDER BY due.next_fetch_at ASC`,
    )
    .bind(nowISO(), limit > 0 ? limit : 25)
    .all<DueStatsRow>();
  return results;
}
