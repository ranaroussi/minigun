package store

import (
	"context"

	"github.com/ranaroussi/minigun/internal/ids"
	"github.com/ranaroussi/minigun/internal/models"
)

func (s *Store) RecordUnsubscribeEvent(ctx context.Context, sendID *string, sub *models.Subscription, email string) (*models.UnsubscribeEvent, error) {
	return s.RecordUnsubscribeEventWithReason(ctx, sendID, sub, email, "")
}

// RecordUnsubscribeEventWithReason is the audit-aware variant used by the
// list-hygiene tooling. reason is one of:
//   ""                            — end-user / unspecified (legacy default)
//   "auto-prune-by-count"         — hygiene tool: messages_since_engagement >= N
//   "auto-prune-by-recency"       — hygiene tool: last_engagement < cutoff
//   "auto-prune-by-no-delivery"   — hygiene tool: never delivered to in window
//   "manual"                      — admin-initiated bulk unsubscribe
func (s *Store) RecordUnsubscribeEventWithReason(ctx context.Context, sendID *string, sub *models.Subscription, email, reason string) (*models.UnsubscribeEvent, error) {
	id := ids.NewUnsub()
	now := nowISO()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var reasonVal any
	if reason != "" {
		reasonVal = reason
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO unsubscribe_events (id, send_id, subscription_id, list_id, contact_id, email, created_at, reason)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, nullString(sendID), sub.ID, sub.ListID, sub.ContactID, email, now, reasonVal,
	); err != nil {
		return nil, err
	}
	if sendID != nil && *sendID != "" {
		if err := s.IncrementSendStatsUnsubscribed(ctx, tx, *sendID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &models.UnsubscribeEvent{
		ID:             id,
		SendID:         sendID,
		SubscriptionID: sub.ID,
		ListID:         sub.ListID,
		ContactID:      sub.ContactID,
		Email:          email,
	}, nil
}

func (s *Store) CountUnsubscribesForSend(ctx context.Context, sendID string) (int, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM unsubscribe_events WHERE send_id = ?`, sendID,
	)
	var n int
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}
