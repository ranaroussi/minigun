-- +goose Up
-- +goose StatementBegin
-- ============================================================================
-- 00007_events_archive
-- ============================================================================
-- Schema foundation for the Mailgun events archive feature.
--
-- This migration is FULLY ADDITIVE:
--   * Two new tables (mailgun_events, contact_engagement).
--   * Six new columns on sends, all with safe defaults.
--   * Six new indexes.
--   * No existing column, row, or index is touched.
--
-- Nothing in this migration activates the feature. The pull cron and the
-- engagement-summary maintenance logic remain dormant until the operator
-- sets EVENTS_ARCHIVE_ENABLED=true at runtime. This phased approach lets
-- the schema land safely well in advance of the consumer code.
-- ============================================================================

-- Raw per-recipient per-event archive. One row per Mailgun event (delivered,
-- opened, clicked, failed, complained, unsubscribed, accepted, stored,
-- rejected). Pulled per-send via Mailgun's tag filter (?tags=s_*). The
-- UNIQUE constraint on mailgun_event_id is the entire idempotency story:
-- INSERT OR IGNORE makes the pull loop safe under Mailgun's retry behavior
-- and our own 6h overlap window.
CREATE TABLE mailgun_events (
  id                   TEXT PRIMARY KEY,                                  -- mge_*
  domain               TEXT NOT NULL,                                     -- sending domain
  mailgun_event_id     TEXT NOT NULL,                                     -- mailgun's stable event id (dedupe key)
  event                TEXT NOT NULL,                                     -- delivered|opened|clicked|failed|complained|unsubscribed|accepted|stored|rejected
  severity             TEXT,                                              -- for 'failed': permanent|temporary; NULL otherwise
  recipient            TEXT NOT NULL,                                     -- always lowercased
  recipient_domain     TEXT NOT NULL,                                     -- denormalized for "all gmail.com complaints" dashboards
  event_timestamp_ms   INTEGER NOT NULL,                                  -- mailgun's event timestamp (epoch ms)
  event_timestamp_iso  TEXT NOT NULL,                                     -- ISO 8601 of event_timestamp_ms
  message_id           TEXT,                                              -- mailgun message-id header (links event to message)
  mg_send_id           TEXT NOT NULL REFERENCES sends(id),                -- the MiniGun send this event belongs to (NOT NULL: only pulled because of it)
  contact_id           TEXT REFERENCES contacts(id) ON DELETE SET NULL,   -- resolved at insert time; NULL'd on hard-delete to preserve forensic data
  url                  TEXT,                                              -- for 'clicked' events
  reason               TEXT,                                              -- for 'failed': SMTP code / bounce description
  tags                 TEXT,                                              -- JSON: mailgun's tag array
  client_info          TEXT,                                              -- JSON: UA, device, OS
  geolocation          TEXT,                                              -- JSON: country/region/city
  user_variables       TEXT,                                              -- JSON: every v:* set at send time
  raw_payload          TEXT NOT NULL,                                     -- full mailgun event JSON (forensic value)
  created_at           TEXT NOT NULL,                                     -- when MiniGun ingested it (distinct from event_timestamp_ms)
  UNIQUE(mailgun_event_id)
);

CREATE INDEX idx_mailgun_events_send
  ON mailgun_events(mg_send_id, event_timestamp_ms DESC);

CREATE INDEX idx_mailgun_events_contact
  ON mailgun_events(contact_id, event_timestamp_ms DESC)
  WHERE contact_id IS NOT NULL;

CREATE INDEX idx_mailgun_events_recipient
  ON mailgun_events(recipient, event_timestamp_ms DESC);

CREATE INDEX idx_mailgun_events_domain_time
  ON mailgun_events(domain, event_timestamp_ms DESC);

CREATE INDEX idx_mailgun_events_msg
  ON mailgun_events(message_id, event)
  WHERE message_id IS NOT NULL;

-- Per-(contact, list) denormalized engagement summary, maintained
-- incrementally on every successful INSERT INTO mailgun_events. This is the
-- table the auto-prune logic reads against — keeping engagement per-list
-- (not global) means a contact dead on list A but engaged on list B stays
-- subscribed to list B. messages_since_last_engagement is the prune-by-count
-- metric; last_engagement_at_ms is the prune-by-recency metric.
--
-- Rows are sparse: no row exists for (contact, list) until we've ingested
-- a delivered event for that pair. This is intentional and protects against
-- false positives on contacts that haven't yet received any messages.
CREATE TABLE contact_engagement (
  contact_id                      TEXT NOT NULL REFERENCES contacts(id) ON DELETE CASCADE,
  list_id                         TEXT NOT NULL REFERENCES lists(id)    ON DELETE CASCADE,
  last_delivered_at_ms            INTEGER,
  last_open_at_ms                 INTEGER,
  last_click_at_ms                INTEGER,
  last_engagement_at_ms           INTEGER,                                -- max(last_open_at_ms, last_click_at_ms)
  total_delivered                 INTEGER NOT NULL DEFAULT 0,
  total_opens                     INTEGER NOT NULL DEFAULT 0,
  total_clicks                    INTEGER NOT NULL DEFAULT 0,
  messages_since_last_engagement  INTEGER NOT NULL DEFAULT 0,              -- prune-by-count metric (reset to 0 on open/click)
  updated_at                      TEXT NOT NULL,
  PRIMARY KEY (contact_id, list_id)
);

-- Hot path for the count-based prune query.
CREATE INDEX idx_engagement_prunable_by_count
  ON contact_engagement(list_id, messages_since_last_engagement DESC);

-- Hot path for the recency-based prune query.
CREATE INDEX idx_engagement_prunable_by_recency
  ON contact_engagement(list_id, last_engagement_at_ms ASC);

-- Per-send bookkeeping for the events-pull cron. Avoids a separate state
-- table by piggybacking on the existing sends row. events_pulls_count is
-- the monotonic counter that drives the burst-vs-daily schedule dispatch;
-- events_archive_complete=1 freezes the send forever (no further polls).
ALTER TABLE sends ADD COLUMN events_last_pulled_at_ms      INTEGER;
ALTER TABLE sends ADD COLUMN events_last_pulled_through_ms INTEGER;
ALTER TABLE sends ADD COLUMN events_pulls_count            INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sends ADD COLUMN events_archive_count          INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sends ADD COLUMN events_archive_complete       INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sends ADD COLUMN events_last_pull_error        TEXT;

-- Cron's only query path: "give me sends that are due for an events pull."
-- The partial-index predicate matches the WHERE clause in pullDueSendEvents,
-- so candidate selection is a direct index scan even when the sends table
-- grows large.
CREATE INDEX idx_sends_event_pull_due
  ON sends(events_last_pulled_at_ms)
  WHERE events_archive_complete = 0 AND test_mode = 0;
-- +goose StatementEnd
