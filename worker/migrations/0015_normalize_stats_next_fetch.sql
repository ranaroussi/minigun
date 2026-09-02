-- ============================================================================
-- 0015_normalize_stats_next_fetch
-- ============================================================================
-- Read-cost fix for listDueSendStats, the every-minute stats cron query.
--
-- next_fetch_at had been written in two formats: applyMailgunStats/force-refresh
-- used JS ISO-8601 ("2026-06-05T12:09:15.905Z") while markSendCompletedForStats
-- used SQLite datetime('now') ("2026-06-05 12:09:15"). To compare them the query
-- wrapped both sides in datetime(), which made idx_send_stats_due non-sargable.
-- Once the sends table grew (welcome-email single sends resumed), the planner
-- flipped to a FULL SCAN of sends every tick — ~9-18k rows read per run,
-- ~26M rows/day, several times the D1 free read cap on its own.
--
-- markSendCompletedForStats now writes ISO too, and listDueSendStats compares
-- next_fetch_at as a raw string. This migration backfills the legacy
-- SQLite-format rows so all values are uniformly ISO, then ANALYZEs so the
-- planner drives off the selective partial index (range + ORDER BY + LIMIT,
-- ~25 rows/run) instead of scanning sends.

UPDATE send_stats
   SET next_fetch_at = strftime('%Y-%m-%dT%H:%M:%fZ', next_fetch_at)
 WHERE next_fetch_at IS NOT NULL
   AND next_fetch_at NOT LIKE '%T%';

ANALYZE send_stats;
