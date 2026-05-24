CREATE TABLE IF NOT EXISTS send_stats (
  send_id           TEXT PRIMARY KEY REFERENCES sends(id) ON DELETE CASCADE,
  sent              INTEGER NOT NULL DEFAULT 0,
  delivered         INTEGER NOT NULL DEFAULT 0,
  opened            INTEGER NOT NULL DEFAULT 0,
  clicked           INTEGER NOT NULL DEFAULT 0,
  failed            INTEGER NOT NULL DEFAULT 0,
  complained        INTEGER NOT NULL DEFAULT 0,
  unsubscribed      INTEGER NOT NULL DEFAULT 0,
  first_fetched_at  TEXT,
  last_fetched_at   TEXT,
  next_fetch_at     TEXT,
  is_final          INTEGER NOT NULL DEFAULT 0,
  fetch_error       TEXT,
  created_at        TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at        TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_send_stats_due
  ON send_stats (next_fetch_at)
  WHERE is_final = 0 AND next_fetch_at IS NOT NULL;
