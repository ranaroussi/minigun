package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/ranaroussi/minigun/internal/ids"
	"github.com/ranaroussi/minigun/internal/models"
)

func (s *Store) CreateBatch(ctx context.Context, sendID string, batchIndex int, startID, endID int64, recipientCount int) (*models.SendBatch, error) {
	id := ids.NewBatch()
	now := nowISO()
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO send_batches (id, send_id, batch_index, start_subscription_id, end_subscription_id, recipient_count, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, sendID, batchIndex, startID, endID, recipientCount, models.BatchStatusInFlight, now, now,
	)
	if err != nil {
		return nil, err
	}
	return s.GetBatch(ctx, id)
}

func (s *Store) GetBatch(ctx context.Context, id string) (*models.SendBatch, error) {
	row := s.DB.QueryRowContext(ctx, `
		SELECT id, send_id, batch_index, start_subscription_id, end_subscription_id, recipient_count, status, mailgun_response, created_at, updated_at
		FROM send_batches WHERE id = ?`, id,
	)
	var b models.SendBatch
	var resp sql.NullString
	var created, updated string
	if err := row.Scan(&b.ID, &b.SendID, &b.BatchIndex, &b.StartSubscriptionID, &b.EndSubscriptionID, &b.RecipientCount, &b.Status, &resp, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	b.MailgunResponse = stringPtr(resp)
	var err error
	if b.CreatedAt, err = parseTime(created); err != nil {
		return nil, err
	}
	if b.UpdatedAt, err = parseTime(updated); err != nil {
		return nil, err
	}
	return &b, nil
}

func (s *Store) MarkBatchStatus(ctx context.Context, id string, status models.BatchStatus, response *string) error {
	now := nowISO()
	_, err := s.DB.ExecContext(ctx,
		`UPDATE send_batches SET status = ?, mailgun_response = ?, updated_at = ? WHERE id = ?`,
		status, nullString(response), now, id,
	)
	return err
}

func (s *Store) NextBatchIndex(ctx context.Context, sendID string) (int, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(batch_index), -1) + 1 FROM send_batches WHERE send_id = ?`, sendID,
	)
	var n int
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

func (s *Store) SendProgress(ctx context.Context, sendID string) (completed int, sent int, err error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(recipient_count), 0)
		 FROM send_batches WHERE send_id = ? AND status = 'succeeded'`, sendID,
	)
	if err := row.Scan(&completed, &sent); err != nil {
		return 0, 0, err
	}
	return completed, sent, nil
}
