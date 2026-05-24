package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/ranaroussi/minigun/internal/models"
)

type SendSummary struct {
	ID              string            `json:"id"`
	Type            models.SendType   `json:"type"`
	ListID          *string           `json:"list_id,omitempty"`
	RecipientEmail  *string           `json:"recipient_email,omitempty"`
	Subject         string            `json:"subject"`
	Status          models.SendStatus `json:"status"`
	TotalRecipients int               `json:"total_recipients"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
	CompletedAt     *time.Time        `json:"completed_at,omitempty"`
}

func (s *Store) ListSends(ctx context.Context, afterCreatedAt string, afterID string, limit int) ([]SendSummary, error) {
	const baseWhere = `
		WHERE (? = '' OR created_at < ? OR (created_at = ? AND id < ?))`
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, type, list_id, recipient_email, subject, status, total_recipients,
		       created_at, updated_at, completed_at
		FROM sends`+baseWhere+`
		ORDER BY created_at DESC, id DESC
		LIMIT ?`,
		afterCreatedAt, afterCreatedAt, afterCreatedAt, afterID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SendSummary
	for rows.Next() {
		var s SendSummary
		var listID, recipEmail sql.NullString
		var created, updated string
		var completed sql.NullString
		if err := rows.Scan(&s.ID, &s.Type, &listID, &recipEmail, &s.Subject, &s.Status, &s.TotalRecipients, &created, &updated, &completed); err != nil {
			return nil, err
		}
		s.ListID = stringPtr(listID)
		s.RecipientEmail = stringPtr(recipEmail)
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
		out = append(out, s)
	}
	return out, rows.Err()
}
