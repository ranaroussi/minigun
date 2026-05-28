-- ============================================================================
-- 0011_message_clicks
-- ============================================================================
-- Phase 7 — D1 mirror of src/internal/db/migrations/00011_message_clicks.sql.
-- See that file for the full rationale.
--
-- Per-URL click rollup, one row per (send_id, contact_id, url). Finer-grained
-- sibling of contact_message_engagement.total_clicks, for audience
-- segmentation ("who clicked this link") and per-link analytics. Bounded by
-- recipients x distinct canonical URLs per message. url is canonical
-- (trimmed, scheme+host lowercased, query + fragment stripped). Timestamps
-- are EPOCH SECONDS.
-- ============================================================================

CREATE TABLE contact_message_clicks (
  send_id        TEXT NOT NULL REFERENCES sends(id)    ON DELETE CASCADE,
  contact_id     TEXT NOT NULL REFERENCES contacts(id) ON DELETE CASCADE,
  list_id        TEXT,  -- NULL for list-less singles (known contacts only)
  url            TEXT NOT NULL,
  first_click_at INTEGER,
  last_click_at  INTEGER,
  total_clicks   INTEGER NOT NULL DEFAULT 0,
  updated_at     INTEGER NOT NULL,
  PRIMARY KEY (send_id, contact_id, url)
);

CREATE INDEX idx_cmc_url
  ON contact_message_clicks(url, list_id);
