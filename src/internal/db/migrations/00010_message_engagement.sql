-- +goose Up
-- +goose StatementBegin
-- ============================================================================
-- 00010_message_engagement
-- ============================================================================
-- Phase 6: replace the per-event archive with two bounded engagement
-- rollups, and drop the raw event ledger entirely.
--
-- Rationale: a send can produce unboundedly many raw events (one contact
-- can open/click many times), so the old mailgun_events table grew with
-- total events. We don't need that — every metric we care about folds
-- into at most one row per recipient. This migration:
--
--   1. NEW contact_message_engagement — one row per (send_id, contact_id):
--      sent/delivered timestamps, first/last open + click with counts,
--      failure/complaint/unsubscribe state. Bounded to <= recipients/send.
--      Timestamps are EPOCH SECONDS (deliberate unit split from
--      contact_engagement's _ms columns; contained to the ingest boundary).
--
--   2. DROP mailgun_events — no raw per-event rows are kept. The pull loop
--      is incremental (begin = max event timestamp from the previous pull,
--      defaulting to the send's created_at on the first pull), so events
--      are seen once and folded straight into the two rollups. There is no
--      dedup ledger, no engagement_applied replay queue, and no per-event
--      timeline read surface.
--
-- contact_engagement (the per-(contact, list) lifetime rollup) is created
-- in 00007 and continues to be maintained incrementally by the pull loop.
-- ============================================================================

CREATE TABLE contact_message_engagement (
  send_id            TEXT NOT NULL REFERENCES sends(id)    ON DELETE CASCADE,
  contact_id         TEXT NOT NULL REFERENCES contacts(id) ON DELETE CASCADE,
  list_id            TEXT,                                  -- NULL for list-less singles (only when the recipient is already a known contact; one-off sends to unknown addresses are not rolled up)
  sent_at            INTEGER,                               -- epoch seconds; from the 'accepted' event
  delivered_at       INTEGER,                               -- epoch seconds; from the 'delivered' event
  first_open_at      INTEGER,
  last_open_at       INTEGER,
  total_opens        INTEGER NOT NULL DEFAULT 0,
  first_click_at     INTEGER,
  last_click_at      INTEGER,
  total_clicks       INTEGER NOT NULL DEFAULT 0,
  failed             INTEGER NOT NULL DEFAULT 0,
  failed_at          INTEGER,                               -- last failure wins
  failure_severity   TEXT,                                  -- permanent|temporary (last failure)
  failure_reason     TEXT,                                  -- SMTP/bounce description (last failure)
  complained_at      INTEGER,
  unsubscribed_at    INTEGER,
  replied_at         INTEGER,                               -- reserved; not populated by Mailgun events
  updated_at         INTEGER NOT NULL,                      -- epoch seconds
  PRIMARY KEY (send_id, contact_id)
);

-- Per-contact lookups across that contact's messages (e.g. "show me how
-- alice engaged with every send on this list").
CREATE INDEX idx_cme_contact_list
  ON contact_message_engagement(contact_id, list_id);

-- Drop the raw event ledger. With an incremental, watermark-driven pull
-- there are no duplicates to dedup against and no timeline to serve, so
-- the table has no remaining purpose. DROP TABLE drops its indexes too.
DROP TABLE IF EXISTS mailgun_events;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS contact_message_engagement;
-- mailgun_events is not recreated: it held raw per-event data that can't
-- be reconstructed, and the read/ingest code no longer references it.
-- +goose StatementEnd
