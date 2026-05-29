-- Adds scheduled (future-dated) sends. send_at is a nullable ISO-8601 UTC
-- timestamp; when set on creation the send is parked in the new 'scheduled'
-- status instead of being dispatched immediately. The cron dispatcher
-- (send/cron.ts dispatchDueSends) sweeps for due rows
-- (status='scheduled' AND send_at <= now) every tick and kicks them through
-- POST /send/:id/next, where they transition to 'running'.
--
-- Unschedule is a one-row update (status -> 'cancelled') guarded to the
-- pre-dispatch states, so cancellation never races a send already in flight.
--
-- The composite index serves the dispatcher's hot query: an equality on
-- status with a range scan on send_at.

ALTER TABLE sends ADD COLUMN send_at TEXT;

CREATE INDEX idx_sends_due ON sends(status, send_at);
