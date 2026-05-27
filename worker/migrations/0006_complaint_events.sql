-- Records spam-complaint events received via Mailgun webhooks.
-- Distinct from unsubscribe_events: complaints are a stronger signal
-- (FBL / "Report Spam" button) and trigger a full contact delete, so
-- this table is the only surviving evidence that the complaint
-- happened. Mailgun's event id is captured to make webhook retries
-- idempotent at the storage layer.
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
