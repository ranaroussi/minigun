-- ============================================================================
-- 0013_cron_read_indexes
-- ============================================================================
-- Purely additive: two partial indexes that eliminate full-table scans run by
-- the every-minute scheduled() cron. No column, row, or existing index is
-- touched, and no application code changes.
--
-- Before: on a database grown to thousands of `sends`/`send_batches` rows
-- (every welcome single-send adds one of each), the per-tick cron scanned
-- ~14M rows/day — over Cloudflare D1's Workers Free 5M rows/day read cap.
-- The waste came from unindexed status filters and candidate scans that
-- dragged across thousands of single-send rows the pulls never touch.
--
--   * reclaimStuckBatches  — UPDATE ... WHERE status='in_flight' AND updated_at<?
--   * listStuckSends       — ... id NOT IN (SELECT send_id FROM send_batches
--                                            WHERE status='in_flight')
--   * listDueEventPulls    — candidates WHERE events_archive_complete=0
--                            AND test_mode=0 AND type='bulk' AND status IN (...)
-- ============================================================================

-- in_flight batches are transient (flipped in just before the Mailgun call and
-- out again right after) and rare, so this partial index is near-empty. It
-- serves both the reclaim UPDATE's predicate (status='in_flight' AND updated_at
-- range) and the listStuckSends anti-join subquery (status='in_flight'),
-- turning two full send_batches SCANs per tick into ~0-row index lookups.
CREATE INDEX IF NOT EXISTS idx_send_batches_in_flight
  ON send_batches(updated_at)
  WHERE status = 'in_flight';

-- The events-pull candidate set is only non-frozen BULK sends (a handful),
-- but without `type` in the predicate the planner scanned every completed
-- send — thousands of single sends included. Encoding the exact
-- listDueEventPulls filter keeps the scan proportional to real work.
--
-- The index is keyed on `status` (not events_last_pulled_at_ms) on purpose:
-- SQLite only prefers a partial index when its leading column appears in the
-- query's WHERE, and listDueEventPulls filters `status IN (...)`. Keying on a
-- column absent from the WHERE left the planner on the full-table status
-- index instead. Run ANALYZE after creating so the planner has row counts.
CREATE INDEX IF NOT EXISTS idx_sends_pull_due_bulk
  ON sends(status)
  WHERE events_archive_complete = 0 AND test_mode = 0 AND type = 'bulk';
