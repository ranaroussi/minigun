-- +goose Up
-- +goose StatementBegin
-- ============================================================================
-- 00009_phase5
-- ============================================================================
-- Phase 5 hardening — fully additive. Three additions:
--
--   1. mailgun_events.engagement_applied — tracks whether the per-(contact,
--      list) engagement summary has been updated for this raw event. When
--      a pull inserts the row but the subsequent engagement UPSERT fails
--      (process crash, transient D1 error, etc.), the row stays at 0 and
--      a periodic replay scanner reconciles it on the next pull tick.
--      Existing rows are backfilled to 1 because Phase 2 already applied
--      them inline; treating them as un-applied would double-count.
--
--   2. idx_mailgun_events_unapplied — partial index over the un-applied
--      replay queue. Without this, the replay query would scan the whole
--      mailgun_events table; with it, the query reads only the (typically
--      empty) pending set.
--
--   3. worker_state — small key/value table used as a persistent
--      sentinel store for cron throttles (e.g., auto-prune-last-run-ms).
--      Cloudflare Workers are stateless per invocation, so any "run at
--      most once per day" semantics need a persistent backing store.
--      Reused on the Go side for parity (Go's in-process timer is robust
--      to a single restart, but cluster failovers would re-fire).
-- ============================================================================
ALTER TABLE mailgun_events ADD COLUMN engagement_applied INTEGER NOT NULL DEFAULT 0;
UPDATE mailgun_events SET engagement_applied = 1;

CREATE INDEX idx_mailgun_events_unapplied
  ON mailgun_events(event_timestamp_ms)
  WHERE engagement_applied = 0;

CREATE TABLE worker_state (
  key        TEXT PRIMARY KEY,
  value      TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS worker_state;
DROP INDEX IF EXISTS idx_mailgun_events_unapplied;
-- SQLite doesn't support DROP COLUMN reliably across versions; leaving
-- engagement_applied in place on rollback is a no-op for downstream code
-- that only reads/writes the column when the feature flag is on.
-- +goose StatementEnd
