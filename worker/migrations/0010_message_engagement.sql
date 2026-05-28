-- ============================================================================
-- 0010_message_engagement
-- ============================================================================
-- Phase 6 — D1 mirror of src/internal/db/migrations/00010_message_engagement.sql.
-- See that file for the full rationale.
--
--   1. NEW contact_message_engagement — one row per (send_id, contact_id):
--      sent/delivered, first/last open + click with counts, failure/
--      complaint/unsubscribe state. Bounded to <= recipients/send.
--      Timestamps are EPOCH SECONDS.
--   2. DROP mailgun_events — the incremental, watermark-driven pull folds
--      each event straight into the two engagement rollups; there is no
--      raw event ledger, dedup queue, or per-event timeline surface.
-- ============================================================================

CREATE TABLE contact_message_engagement (
  send_id            TEXT NOT NULL REFERENCES sends(id)    ON DELETE CASCADE,
  contact_id         TEXT NOT NULL REFERENCES contacts(id) ON DELETE CASCADE,
  list_id            TEXT,
  sent_at            INTEGER,
  delivered_at       INTEGER,
  first_open_at      INTEGER,
  last_open_at       INTEGER,
  total_opens        INTEGER NOT NULL DEFAULT 0,
  first_click_at     INTEGER,
  last_click_at      INTEGER,
  total_clicks       INTEGER NOT NULL DEFAULT 0,
  failed             INTEGER NOT NULL DEFAULT 0,
  failed_at          INTEGER,
  failure_severity   TEXT,
  failure_reason     TEXT,
  complained_at      INTEGER,
  unsubscribed_at    INTEGER,
  replied_at         INTEGER,
  updated_at         INTEGER NOT NULL,
  PRIMARY KEY (send_id, contact_id)
);

CREATE INDEX idx_cme_contact_list
  ON contact_message_engagement(contact_id, list_id);

DROP TABLE IF EXISTS mailgun_events;
