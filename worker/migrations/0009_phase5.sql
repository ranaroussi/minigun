-- ============================================================================
-- 0009_phase5
-- ============================================================================
-- Phase 5 hardening — fully additive. See
-- src/internal/db/migrations/00009_phase5.sql for the full rationale.
-- This file is the D1 mirror with identical schema.
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
