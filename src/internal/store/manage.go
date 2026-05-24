package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/ranaroussi/minigun/internal/models"
)

type ManageListState struct {
	List         models.List `json:"list"`
	Subscribed   bool        `json:"subscribed"`
	SubscribedAt *time.Time  `json:"subscribed_at,omitempty"`
}

func (s *Store) GetCompanyListsWithSubscription(ctx context.Context, companyID, contactID string) ([]ManageListState, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT l.id, l.slug, l.name, COALESCE(l.description, ''), COALESCE(l.weight, 10),
		       COALESCE(l.company_id, ''), l.created_at, l.updated_at,
		       subs.subscribed, subs.subscribed_at
		FROM lists l
		LEFT JOIN subscriptions subs ON subs.list_id = l.id AND subs.contact_id = ?
		WHERE l.company_id = ?
		ORDER BY l.weight ASC, l.name ASC`,
		contactID, companyID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ManageListState
	for rows.Next() {
		var st ManageListState
		var l models.List
		var created, updated string
		var sub sql.NullInt64
		var subAt sql.NullString
		if err := rows.Scan(&l.ID, &l.Slug, &l.Name, &l.Description, &l.Weight, &l.CompanyID, &created, &updated, &sub, &subAt); err != nil {
			return nil, err
		}
		if l.CreatedAt, err = parseTime(created); err != nil {
			return nil, err
		}
		if l.UpdatedAt, err = parseTime(updated); err != nil {
			return nil, err
		}
		st.List = l
		st.Subscribed = sub.Valid && sub.Int64 == 1
		if st.SubscribedAt, err = parseTimePtr(subAt); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

type SubscriptionChange struct {
	ListID     string
	Subscribed bool
}

type SubscriptionDelta struct {
	ListID     string
	ListName   string
	WasSubbed  bool
	NowSubbed  bool
}

func (s *Store) ApplySubscriptionChanges(ctx context.Context, contactID string, desired []SubscriptionChange) ([]SubscriptionDelta, error) {
	if len(desired) == 0 {
		return nil, nil
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	now := nowISO()
	deltas := make([]SubscriptionDelta, 0, len(desired))
	for _, ch := range desired {
		var listName string
		if err := tx.QueryRowContext(ctx, `SELECT name FROM lists WHERE id = ?`, ch.ListID).Scan(&listName); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return nil, err
		}
		existing, err := getSubscriptionTx(ctx, tx, ch.ListID, contactID)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return nil, err
		}

		var wasSubbed bool
		if existing != nil {
			wasSubbed = existing.Subscribed
		}
		if wasSubbed == ch.Subscribed && existing != nil {
			continue
		}

		if existing == nil {
			if !ch.Subscribed {
				continue
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO subscriptions (list_id, contact_id, subscribed, subscribed_at, updated_at) VALUES (?, ?, 1, ?, ?)`,
				ch.ListID, contactID, now, now,
			); err != nil {
				return nil, err
			}
		} else if ch.Subscribed {
			if _, err := tx.ExecContext(ctx,
				`UPDATE subscriptions SET subscribed = 1, subscribed_at = ?, unsubscribed_at = NULL, updated_at = ? WHERE id = ?`,
				now, now, existing.ID,
			); err != nil {
				return nil, err
			}
		} else {
			if _, err := tx.ExecContext(ctx,
				`UPDATE subscriptions SET subscribed = 0, unsubscribed_at = ?, updated_at = ? WHERE id = ?`,
				now, now, existing.ID,
			); err != nil {
				return nil, err
			}
		}
		deltas = append(deltas, SubscriptionDelta{
			ListID:    ch.ListID,
			ListName:  listName,
			WasSubbed: wasSubbed,
			NowSubbed: ch.Subscribed,
		})
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return deltas, nil
}
