package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

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
	BatchSize              int
	ThrottleMS             int
	MaxSubscriptionID      *int64
	TotalRecipients        int
	UnsubscribeMode        models.UnsubscribeMode
	UnsubscribeRedirectURL *string
	UnsubscribeExternalURL *string
	NotifyEmail            *string
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

	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO sends (
			id, type, list_id, recipient_email, subject, from_header, reply_to, template_name,
			body_md, body_html, body_text,
			status, batch_size, throttle_ms,
			last_subscription_id, max_subscription_id, total_recipients,
			unsubscribe_mode, unsubscribe_redirect_url, unsubscribe_external_url,
			notify_email, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, p.Type, p.ListID, p.RecipientEmail, p.Subject, p.FromHeader, nullString(p.ReplyTo), nullString(p.TemplateName),
		nullString(p.BodyMD), nullString(p.BodyHTML), nullString(p.BodyText),
		models.SendStatusQueued, p.BatchSize, p.ThrottleMS,
		p.MaxSubscriptionID, p.TotalRecipients,
		p.UnsubscribeMode, nullString(p.UnsubscribeRedirectURL), nullString(p.UnsubscribeExternalURL),
		nullString(p.NotifyEmail), now, now,
	)
	if err != nil {
		return nil, fmt.Errorf("insert send: %w", err)
	}
	return s.GetSend(ctx, id)
}

func (s *Store) GetSend(ctx context.Context, id string) (*models.Send, error) {
	row := s.DB.QueryRowContext(ctx, `
		SELECT id, type, list_id, recipient_email, subject, from_header, reply_to, template_name,
		       body_md, body_html, body_text,
		       status, batch_size, throttle_ms,
		       last_subscription_id, max_subscription_id, total_recipients,
		       unsubscribe_mode, unsubscribe_redirect_url, unsubscribe_external_url,
		       notify_email, last_error, created_at, updated_at, completed_at
		FROM sends WHERE id = ?`, id,
	)
	return scanSend(row)
}

func scanSend(row *sql.Row) (*models.Send, error) {
	var s models.Send
	var listID, recipEmail, replyTo, tmpl, bodyMD, bodyHTML, bodyText sql.NullString
	var unsubRedir, unsubExt, notifyEmail, lastErr sql.NullString
	var maxSubID sql.NullInt64
	var created, updated string
	var completed sql.NullString

	if err := row.Scan(
		&s.ID, &s.Type, &listID, &recipEmail, &s.Subject, &s.FromHeader, &replyTo, &tmpl,
		&bodyMD, &bodyHTML, &bodyText,
		&s.Status, &s.BatchSize, &s.ThrottleMS,
		&s.LastSubscriptionID, &maxSubID, &s.TotalRecipients,
		&s.UnsubscribeMode, &unsubRedir, &unsubExt,
		&notifyEmail, &lastErr, &created, &updated, &completed,
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
	_, err := s.DB.ExecContext(ctx,
		`UPDATE sends SET status = ?, last_error = ?, updated_at = ?, completed_at = COALESCE(?, completed_at) WHERE id = ?`,
		status, nullString(lastErr), now, completedAt, id,
	)
	return err
}

func (s *Store) AdvanceSendCursor(ctx context.Context, id string, lastSubID int64) error {
	now := nowISO()
	_, err := s.DB.ExecContext(ctx,
		`UPDATE sends SET last_subscription_id = ?, updated_at = ? WHERE id = ?`,
		lastSubID, now, id,
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
