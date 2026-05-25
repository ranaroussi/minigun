package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type ListSummary struct {
	ID              string    `json:"id"`
	Slug            string    `json:"slug"`
	Name            string    `json:"name"`
	Description     string    `json:"description"`
	Weight          int       `json:"weight"`
	CompanyID       string    `json:"company_id"`
	SendingDomain   string    `json:"sending_domain"`
	SubscribedCount int       `json:"subscribed_count"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type ListDetails struct {
	ListSummary
	TotalCount int        `json:"total_count"`
	LastSendAt *time.Time `json:"last_send_at,omitempty"`
}

func (s *Store) ListLists(ctx context.Context) ([]ListSummary, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT l.id, l.slug, l.name, COALESCE(l.description, ''), COALESCE(l.weight, 10),
		       COALESCE(l.company_id, ''), l.sending_domain, l.created_at, l.updated_at,
		       COALESCE(SUM(CASE WHEN subs.subscribed = 1 THEN 1 ELSE 0 END), 0) AS subscribed_count
		FROM lists l
		LEFT JOIN subscriptions subs ON subs.list_id = l.id
		GROUP BY l.id
		ORDER BY l.weight ASC, l.name ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ListSummary
	for rows.Next() {
		var l ListSummary
		var created, updated string
		if err := rows.Scan(&l.ID, &l.Slug, &l.Name, &l.Description, &l.Weight, &l.CompanyID, &l.SendingDomain, &created, &updated, &l.SubscribedCount); err != nil {
			return nil, err
		}
		if l.CreatedAt, err = parseTime(created); err != nil {
			return nil, err
		}
		if l.UpdatedAt, err = parseTime(updated); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *Store) GetListDetails(ctx context.Context, listID string) (*ListDetails, error) {
	row := s.DB.QueryRowContext(ctx, `
		SELECT l.id, l.slug, l.name, COALESCE(l.description, ''), COALESCE(l.weight, 10),
		       COALESCE(l.company_id, ''), l.sending_domain, l.created_at, l.updated_at,
		       COALESCE(SUM(CASE WHEN subs.subscribed = 1 THEN 1 ELSE 0 END), 0) AS subscribed_count,
		       COUNT(subs.id) AS total_count
		FROM lists l
		LEFT JOIN subscriptions subs ON subs.list_id = l.id
		WHERE l.id = ?
		GROUP BY l.id`, listID,
	)
	var d ListDetails
	var created, updated string
	if err := row.Scan(&d.ID, &d.Slug, &d.Name, &d.Description, &d.Weight, &d.CompanyID, &d.SendingDomain, &created, &updated, &d.SubscribedCount, &d.TotalCount); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var err error
	if d.CreatedAt, err = parseTime(created); err != nil {
		return nil, err
	}
	if d.UpdatedAt, err = parseTime(updated); err != nil {
		return nil, err
	}

	var lastSend sql.NullString
	if err := s.DB.QueryRowContext(ctx,
		`SELECT MAX(created_at) FROM sends WHERE list_id = ?`, listID,
	).Scan(&lastSend); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if d.LastSendAt, err = parseTimePtr(lastSend); err != nil {
		return nil, err
	}
	return &d, nil
}

type ListContact struct {
	SubscriptionID int64      `json:"subscription_id"`
	ContactID      string     `json:"contact_id"`
	Email          string     `json:"email"`
	Params         string     `json:"params"`
	Subscribed     bool       `json:"subscribed"`
	SubscribedAt   *time.Time `json:"subscribed_at,omitempty"`
	UnsubscribedAt *time.Time `json:"unsubscribed_at,omitempty"`
}

func (s *Store) ListContactsInList(ctx context.Context, listID string, afterSubID int64, limit int) ([]ListContact, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT subs.id, c.id, c.email, c.params, subs.subscribed, subs.subscribed_at, subs.unsubscribed_at
		FROM subscriptions subs
		JOIN contacts c ON c.id = subs.contact_id
		WHERE subs.list_id = ? AND subs.id > ?
		ORDER BY subs.id ASC
		LIMIT ?`,
		listID, afterSubID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ListContact
	for rows.Next() {
		var c ListContact
		var sub int
		var subAt, unsubAt sql.NullString
		if err := rows.Scan(&c.SubscriptionID, &c.ContactID, &c.Email, &c.Params, &sub, &subAt, &unsubAt); err != nil {
			return nil, err
		}
		c.Subscribed = sub == 1
		var err error
		if c.SubscribedAt, err = parseTimePtr(subAt); err != nil {
			return nil, err
		}
		if c.UnsubscribedAt, err = parseTimePtr(unsubAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
