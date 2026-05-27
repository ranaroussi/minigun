-- +goose Up
-- +goose StatementBegin
CREATE TABLE complaint_events (
  id TEXT PRIMARY KEY,
  email TEXT NOT NULL,
  contact_id TEXT,
  mailgun_event_id TEXT UNIQUE,
  mailgun_timestamp TEXT,
  payload TEXT,
  created_at TEXT NOT NULL
);
CREATE INDEX idx_complaint_events_email ON complaint_events(email);
CREATE INDEX idx_complaint_events_created ON complaint_events(created_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_complaint_events_created;
DROP INDEX IF EXISTS idx_complaint_events_email;
DROP TABLE IF EXISTS complaint_events;
-- +goose StatementEnd
