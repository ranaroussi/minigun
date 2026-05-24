CREATE TABLE lists (
  id TEXT PRIMARY KEY,
  slug TEXT NOT NULL UNIQUE,
  name TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE contacts (
  id TEXT PRIMARY KEY,
  email TEXT NOT NULL UNIQUE,
  params TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE subscriptions (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  list_id TEXT NOT NULL REFERENCES lists(id),
  contact_id TEXT NOT NULL REFERENCES contacts(id),
  subscribed INTEGER NOT NULL DEFAULT 1,
  subscribed_at TEXT,
  updated_at TEXT NOT NULL,
  unsubscribed_at TEXT,
  UNIQUE(list_id, contact_id)
);

CREATE INDEX idx_subscriptions_list_subscribed
  ON subscriptions(list_id, subscribed, id);

CREATE TABLE sends (
  id TEXT PRIMARY KEY,
  type TEXT NOT NULL,
  list_id TEXT REFERENCES lists(id),
  recipient_email TEXT,
  subject TEXT NOT NULL,
  from_header TEXT NOT NULL,
  reply_to TEXT,
  template_name TEXT,
  body_md TEXT,
  body_html TEXT,
  body_text TEXT,
  status TEXT NOT NULL,
  batch_size INTEGER NOT NULL DEFAULT 500,
  throttle_ms INTEGER NOT NULL DEFAULT 1000,
  last_subscription_id INTEGER NOT NULL DEFAULT 0,
  max_subscription_id INTEGER,
  total_recipients INTEGER DEFAULT 0,
  unsubscribe_mode TEXT NOT NULL DEFAULT 'local',
  unsubscribe_redirect_url TEXT,
  unsubscribe_external_url TEXT,
  notify_email TEXT,
  last_error TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  completed_at TEXT
);

CREATE INDEX idx_sends_status ON sends(status);

CREATE TABLE send_batches (
  id TEXT PRIMARY KEY,
  send_id TEXT NOT NULL REFERENCES sends(id),
  batch_index INTEGER NOT NULL,
  start_subscription_id INTEGER NOT NULL,
  end_subscription_id INTEGER NOT NULL,
  recipient_count INTEGER NOT NULL,
  status TEXT NOT NULL,
  mailgun_response TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE INDEX idx_send_batches_send ON send_batches(send_id, batch_index);

CREATE TABLE unsubscribe_events (
  id TEXT PRIMARY KEY,
  send_id TEXT,
  subscription_id INTEGER NOT NULL,
  list_id TEXT NOT NULL,
  contact_id TEXT NOT NULL,
  email TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE INDEX idx_unsub_events_send ON unsubscribe_events(send_id);
CREATE INDEX idx_unsub_events_list ON unsubscribe_events(list_id);
