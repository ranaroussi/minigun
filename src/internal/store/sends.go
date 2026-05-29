package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ranaroussi/minigun/internal/ids"
	"github.com/ranaroussi/minigun/internal/models"
)

type NewSendParams struct {
	Type                   models.SendType
	ListID                 *string
	RecipientEmail         *string
	Subject                string
	FromHeader             string
	ReplyTo                *string
	TemplateName           *string
	BodyMD                 *string
	BodyHTML               *string
	BodyText               *string
	SendingDomain          string
	BatchSize              int
	ThrottleMS             int
	TestMode               bool
	// LastSubscriptionID has two meanings depending on send type:
	//   bulk:   cursor — the highest subscription_id already processed (starts at 0)
	//   single: the recipient's own subscription_id when the caller passed a list,
	//           so the send-time worker can sign a per-recipient unsubscribe token.
	//           Zero means "no list was tied to this single send" → no auto-unsub link.
	LastSubscriptionID     int64
	MaxSubscriptionID      *int64
	TotalRecipients        int
	UnsubscribeMode        models.UnsubscribeMode
	UnsubscribeRedirectURL *string
	UnsubscribeExternalURL *string
	NotifyEmail            *string
	// SendAt, when non-nil and in the future, parks the send in the
	// 'scheduled' status for the dispatcher to pick up at that time rather
	// than dispatching it immediately. A nil or past value sends now.
	SendAt *time.Time
}

func (s *Store) CreateSend(ctx context.Context, p NewSendParams) (*models.Send, error) {
	id := ids.NewSend()
	now := nowISO()
	if p.BatchSize <= 0 {
		p.BatchSize = 500
	}
	if p.ThrottleMS < 0 {
		p.ThrottleMS = 1000
	}
	if p.UnsubscribeMode == "" {
		p.UnsubscribeMode = models.UnsubModeLocal
	}

	// Schedule only when send_at is in the future; a nil or past value
	// sends immediately (status 'queued', send_at left NULL). send_at is
	// stored at second precision so the dispatcher's lexical string
	// comparison against `now` is exactly chronological.
	status := models.SendStatusQueued
	var sendAt any
	if p.SendAt != nil && p.SendAt.After(time.Now()) {
		status = models.SendStatusScheduled
		sendAt = p.SendAt.UTC().Format(time.RFC3339)
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO sends (
			id, type, list_id, recipient_email, subject, from_header, reply_to, template_name,
			body_md, body_html, body_text, sending_domain,
			status, batch_size, throttle_ms, test_mode,
			last_subscription_id, max_subscription_id, total_recipients,
			unsubscribe_mode, unsubscribe_redirect_url, unsubscribe_external_url,
			notify_email, send_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, p.Type, p.ListID, p.RecipientEmail, p.Subject, p.FromHeader, nullString(p.ReplyTo), nullString(p.TemplateName),
		nullString(p.BodyMD), nullString(p.BodyHTML), nullString(p.BodyText), p.SendingDomain,
		status, p.BatchSize, p.ThrottleMS, p.TestMode,
		p.LastSubscriptionID, p.MaxSubscriptionID, p.TotalRecipients,
		p.UnsubscribeMode, nullString(p.UnsubscribeRedirectURL), nullString(p.UnsubscribeExternalURL),
		nullString(p.NotifyEmail), sendAt, now, now,
	); err != nil {
		return nil, fmt.Errorf("insert send: %w", err)
	}
	if err := s.initSendStatsRow(ctx, tx, id); err != nil {
		return nil, fmt.Errorf("insert send_stats: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return s.GetSend(ctx, id)
}

func (s *Store) GetSend(ctx context.Context, id string) (*models.Send, error) {
	row := s.DB.QueryRowContext(ctx, `
		SELECT id, type, list_id, recipient_email, subject, from_header, reply_to, template_name,
		       body_md, body_html, body_text, sending_domain,
		       status, batch_size, throttle_ms, test_mode,
		       last_subscription_id, max_subscription_id, total_recipients,
		       unsubscribe_mode, unsubscribe_redirect_url, unsubscribe_external_url,
		       notify_email, send_at, last_error, created_at, updated_at, completed_at
		FROM sends WHERE id = ?`, id,
	)
	return scanSend(row)
}

func scanSend(row *sql.Row) (*models.Send, error) {
	var s models.Send
	var listID, recipEmail, replyTo, tmpl, bodyMD, bodyHTML, bodyText sql.NullString
	var unsubRedir, unsubExt, notifyEmail, sendAt, lastErr sql.NullString
	var maxSubID sql.NullInt64
	var created, updated string
	var completed sql.NullString

	if err := row.Scan(
		&s.ID, &s.Type, &listID, &recipEmail, &s.Subject, &s.FromHeader, &replyTo, &tmpl,
		&bodyMD, &bodyHTML, &bodyText, &s.SendingDomain,
		&s.Status, &s.BatchSize, &s.ThrottleMS, &s.TestMode,
		&s.LastSubscriptionID, &maxSubID, &s.TotalRecipients,
		&s.UnsubscribeMode, &unsubRedir, &unsubExt,
		&notifyEmail, &sendAt, &lastErr, &created, &updated, &completed,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	s.ListID = stringPtr(listID)
	s.RecipientEmail = stringPtr(recipEmail)
	s.ReplyTo = stringPtr(replyTo)
	s.TemplateName = stringPtr(tmpl)
	s.BodyMD = stringPtr(bodyMD)
	s.BodyHTML = stringPtr(bodyHTML)
	s.BodyText = stringPtr(bodyText)
	s.MaxSubscriptionID = int64Ptr(maxSubID)
	s.UnsubscribeRedirectURL = stringPtr(unsubRedir)
	s.UnsubscribeExternalURL = stringPtr(unsubExt)
	s.NotifyEmail = stringPtr(notifyEmail)
	s.LastError = stringPtr(lastErr)
	var err error
	if s.SendAt, err = parseTimePtr(sendAt); err != nil {
		return nil, err
	}
	if s.CreatedAt, err = parseTime(created); err != nil {
		return nil, err
	}
	if s.UpdatedAt, err = parseTime(updated); err != nil {
		return nil, err
	}
	if s.CompletedAt, err = parseTimePtr(completed); err != nil {
		return nil, err
	}
	return &s, nil
}

func (s *Store) UpdateSendStatus(ctx context.Context, id string, status models.SendStatus, lastErr *string) error {
	now := nowISO()
	var completedAt any
	if status == models.SendStatusCompleted || status == models.SendStatusFailed || status == models.SendStatusCancelled {
		completedAt = now
	}
	if _, err := s.DB.ExecContext(ctx,
		`UPDATE sends SET status = ?, last_error = ?, updated_at = ?, completed_at = COALESCE(?, completed_at) WHERE id = ?`,
		status, nullString(lastErr), now, completedAt, id,
	); err != nil {
		return err
	}
	if completedAt != nil {
		if err := s.MarkSendCompletedForStats(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) AdvanceSendCursor(ctx context.Context, id string, lastSubID int64) error {
	now := nowISO()
	_, err := s.DB.ExecContext(ctx,
		`UPDATE sends SET last_subscription_id = ?, updated_at = ? WHERE id = ?`,
		lastSubID, now, id,
	)
	return err
}

// SetSendAudience freezes a bulk send's recipient set by recording the
// upper subscription-id bound and the resolved recipient count. For
// immediate sends this is captured at creation; for scheduled sends it's
// deferred to dispatch so everyone subscribed up to go-time is included.
func (s *Store) SetSendAudience(ctx context.Context, id string, maxSubID int64, total int) error {
	now := nowISO()
	_, err := s.DB.ExecContext(ctx,
		`UPDATE sends SET max_subscription_id = ?, total_recipients = ?, updated_at = ? WHERE id = ?`,
		maxSubID, total, now, id,
	)
	return err
}

func (s *Store) ListRunningSends(ctx context.Context) ([]*models.Send, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id FROM sends WHERE status IN ('queued', 'running') ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.Send
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		snd, err := s.GetSend(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, snd)
	}
	return out, rows.Err()
}

func (s *Store) HasInFlightBatch(ctx context.Context, sendID string) (bool, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM send_batches WHERE send_id = ? AND status = 'in_flight'`, sendID,
	)
	var n int
	if err := row.Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// ListDueScheduledSends returns the ids of scheduled sends whose send_at has
// arrived (send_at <= now), oldest first. send_at is compared as a fixed
// second-precision RFC3339 string, so lexical order is chronological order.
func (s *Store) ListDueScheduledSends(ctx context.Context, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 100
	}
	now := time.Now().UTC().Format(time.RFC3339)
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id FROM sends
		 WHERE status = 'scheduled' AND send_at IS NOT NULL AND send_at <= ?
		 ORDER BY send_at ASC
		 LIMIT ?`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// CancelScheduledSend transitions a send to 'cancelled', but only from the
// pre-dispatch states ('scheduled' or 'queued'). The guarded WHERE makes
// this race-safe against the dispatcher: if the send started in the gap
// between a status read and this call, zero rows update and cancelled=false.
func (s *Store) CancelScheduledSend(ctx context.Context, id string) (bool, error) {
	now := nowISO()
	res, err := s.DB.ExecContext(ctx, `
		UPDATE sends
		   SET status = 'cancelled', updated_at = ?, completed_at = ?
		 WHERE id = ? AND status IN ('scheduled', 'queued')`,
		now, now, id,
	)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 0 {
		return false, nil
	}
	if err := s.MarkSendCompletedForStats(ctx, id); err != nil {
		return false, err
	}
	return true, nil
}
