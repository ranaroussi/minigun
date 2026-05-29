-- +goose Up
-- +goose StatementBegin
-- ============================================================================
-- 00011_message_clicks
-- ============================================================================
-- Per-URL click rollup, one row per (send_id, contact_id, url). This is the
-- finer-grained sibling of contact_message_engagement.total_clicks: where
-- cme records "contact C clicked something in send S N times", this records
-- "contact C clicked URL U in send S N times". It exists to power audience
-- segmentation ("who clicked this link") and per-link analytics.
--
-- Bounded by recipients x distinct canonical URLs per message — a contact
-- clicking the same link many times is still one row with total_clicks
-- incremented, so there is no raw per-event growth (same philosophy as the
-- rest of the events archive).
--
-- url is stored CANONICAL: trimmed, scheme+host lowercased, query string and
-- fragment removed (see store.canonicalizeClickURL). This collapses tracking
-- params / personalized tokens so segmentation keys on the destination, not
-- on per-recipient link noise.
--
-- Timestamps are EPOCH SECONDS, matching contact_message_engagement.
--
-- Invariant (not enforced): SUM(total_clicks) over a (send_id, contact_id)
-- equals contact_message_engagement.total_clicks for that pair.
-- ============================================================================

CREATE TABLE contact_message_clicks (
  send_id        TEXT NOT NULL REFERENCES sends(id)    ON DELETE CASCADE,
  contact_id     TEXT NOT NULL REFERENCES contacts(id) ON DELETE CASCADE,
  list_id        TEXT,                                  -- NULL for list-less singles (known contacts only)
  url            TEXT NOT NULL,                         -- canonical destination URL
  first_click_at INTEGER,                               -- epoch seconds
  last_click_at  INTEGER,                               -- epoch seconds
  total_clicks   INTEGER NOT NULL DEFAULT 0,
  updated_at     INTEGER NOT NULL,                      -- epoch seconds
  PRIMARY KEY (send_id, contact_id, url)
);

-- Segmentation hot path: "which contacts clicked URL U" (optionally scoped
-- to a list). url leads the index so equality lookups on a destination are
-- a range scan, with list_id available for the common per-list narrowing.
CREATE INDEX idx_cmc_url
  ON contact_message_clicks(url, list_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS contact_message_clicks;
-- +goose StatementEnd
