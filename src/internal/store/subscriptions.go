package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/ranaroussi/minigun/internal/models"
)

func (s *Store) UpsertSubscription(ctx context.Context, listID, contactID string) (*models.Subscription, error) {
	now := nowISO()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	existing, err := getSubscriptionTx(ctx, tx, listID, contactID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	if existing == nil {
		res, err := tx.ExecContext(ctx,
			`INSERT INTO subscriptions (list_id, contact_id, subscribed, subscribed_at, updated_at) VALUES (?, ?, 1, ?, ?)`,
			listID, contactID, now, now,
		)
		if err != nil {
			return nil, err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return s.GetSubscriptionByID(ctx, id)
	}

	if !existing.Subscribed {
		if _, err := tx.ExecContext(ctx,
			`UPDATE subscriptions SET subscribed = 1, subscribed_at = ?, unsubscribed_at = NULL, updated_at = ? WHERE id = ?`,
			now, now, existing.ID,
		); err != nil {
			return nil, err
		}
	} else {
		if _, err := tx.ExecContext(ctx,
			`UPDATE subscriptions SET updated_at = ? WHERE id = ?`,
			now, existing.ID,
		); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetSubscriptionByID(ctx, existing.ID)
}

func (s *Store) UnsubscribeByListAndEmail(ctx context.Context, listID, email string) (*models.Subscription, error) {
	email = NormalizeEmail(email)
	contact, err := s.GetContactByEmail(ctx, email)
	if err != nil {
		return nil, err
	}
	return s.UnsubscribeSubscription(ctx, listID, contact.ID)
}

func (s *Store) UnsubscribeSubscription(ctx context.Context, listID, contactID string) (*models.Subscription, error) {
	now := nowISO()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	sub, err := getSubscriptionTx(ctx, tx, listID, contactID)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE subscriptions SET subscribed = 0, unsubscribed_at = ?, updated_at = ? WHERE id = ?`,
		now, now, sub.ID,
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetSubscriptionByID(ctx, sub.ID)
}

func (s *Store) GetSubscriptionByID(ctx context.Context, id int64) (*models.Subscription, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT id, list_id, contact_id, subscribed, subscribed_at, updated_at, unsubscribed_at FROM subscriptions WHERE id = ?`, id,
	)
	return scanSubscription(row)
}

func (s *Store) GetSubscription(ctx context.Context, listID, contactID string) (*models.Subscription, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT id, list_id, contact_id, subscribed, subscribed_at, updated_at, unsubscribed_at FROM subscriptions WHERE list_id = ? AND contact_id = ?`,
		listID, contactID,
	)
	return scanSubscription(row)
}

func getSubscriptionTx(ctx context.Context, tx *sql.Tx, listID, contactID string) (*models.Subscription, error) {
	row := tx.QueryRowContext(ctx,
		`SELECT id, list_id, contact_id, subscribed, subscribed_at, updated_at, unsubscribed_at FROM subscriptions WHERE list_id = ? AND contact_id = ?`,
		listID, contactID,
	)
	return scanSubscription(row)
}

func scanSubscription(row *sql.Row) (*models.Subscription, error) {
	var sub models.Subscription
	var subscribed int
	var subAt, unsubAt sql.NullString
	var updated string
	if err := row.Scan(&sub.ID, &sub.ListID, &sub.ContactID, &subscribed, &subAt, &updated, &unsubAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	sub.Subscribed = subscribed == 1
	var err error
	if sub.UpdatedAt, err = parseTime(updated); err != nil {
		return nil, err
	}
	if sub.SubscribedAt, err = parseTimePtr(subAt); err != nil {
		return nil, err
	}
	if sub.UnsubscribedAt, err = parseTimePtr(unsubAt); err != nil {
		return nil, err
	}
	return &sub, nil
}

func (s *Store) MaxSubscriptionID(ctx context.Context, listID string) (int64, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(id), 0) FROM subscriptions WHERE list_id = ? AND subscribed = 1`, listID,
	)
	var n int64
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

func (s *Store) CountSubscribed(ctx context.Context, listID string, maxID int64) (int, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM subscriptions WHERE list_id = ? AND subscribed = 1 AND id <= ?`,
		listID, maxID,
	)
	var n int
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

func (s *Store) NextRecipientBatch(ctx context.Context, listID string, afterID, maxID int64, limit int) ([]models.Recipient, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT subs.id, c.id, c.email, c.params
		FROM subscriptions subs
		JOIN contacts c ON c.id = subs.contact_id
		WHERE subs.list_id = ?
		  AND subs.subscribed = 1
		  AND subs.id > ?
		  AND subs.id <= ?
		ORDER BY subs.id ASC
		LIMIT ?`,
		listID, afterID, maxID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.Recipient
	for rows.Next() {
		var r models.Recipient
		if err := rows.Scan(&r.SubscriptionID, &r.ContactID, &r.Email, &r.Params); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
