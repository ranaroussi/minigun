-- +goose Up
-- +goose StatementBegin
-- ============================================================================
-- 00012_scheduled_sends
-- ============================================================================
-- Adds scheduled (future-dated) sends. send_at is a nullable RFC3339 UTC
-- timestamp; when set on creation the send is parked in the new 'scheduled'
-- status instead of being dispatched immediately. A background dispatcher
-- (worker.RunScheduledSendDispatcher) polls for due rows
-- (status='scheduled' AND send_at <= now) and kicks them through the normal
-- run path, at which point they transition to 'running' like any other send.
--
-- Unschedule is a one-row update (status -> 'cancelled') guarded to the
-- pre-dispatch states, so cancellation never races a send already in flight.
--
-- The composite index serves the dispatcher's hot query: an equality on
-- status with a range scan on send_at.
-- ============================================================================
ALTER TABLE sends ADD COLUMN send_at TEXT;

CREATE INDEX idx_sends_due ON sends(status, send_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_sends_due;
ALTER TABLE sends DROP COLUMN send_at;
-- +goose StatementEnd
