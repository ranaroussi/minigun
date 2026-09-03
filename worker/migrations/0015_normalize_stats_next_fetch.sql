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
-- SQLite-format rows so all values are uniformly ISO.
--
-- Note on ANALYZE: it is run below because it is cheap and harmless, but do NOT
-- rely on it. Running it made EXPLAIN pick the right plan under `wrangler d1
-- execute`, yet the deployed worker kept the 18k-row plan afterwards — D1 does
-- not honour these statistics for the Worker's connections. The actual fix is
-- structural: listDueSendStats resolves the due set in a LIMITed subquery over
-- send_stats alone, which SQLite cannot flatten, so sends can never become the
-- driving table (verified: 50 rows/run).
--
-- Correctness note: this backfill is cosmetic/consistency-only. Legacy
-- space-separated values sort BEFORE ISO ones (' ' 0x20 < 'T' 0x54), so they
-- simply always compare as due and self-heal the first time applyMailgunStats
-- rewrites them. The backfill just stops them crowding the LIMIT window (and
-- burning Mailgun calls) until then.

UPDATE send_stats
   SET next_fetch_at = strftime('%Y-%m-%dT%H:%M:%fZ', next_fetch_at)
 WHERE next_fetch_at IS NOT NULL
   AND next_fetch_at NOT LIKE '%T%';

ANALYZE send_stats;
