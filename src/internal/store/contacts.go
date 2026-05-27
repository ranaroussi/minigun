package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/ranaroussi/minigun/internal/ids"
	"github.com/ranaroussi/minigun/internal/models"
)

func NormalizeEmail(e string) string {
	return strings.ToLower(strings.TrimSpace(e))
}

func (s *Store) UpsertContact(ctx context.Context, email string, params map[string]any) (*models.Contact, error) {
	email = NormalizeEmail(email)
	if email == "" {
		return nil, errors.New("email is required")
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	existing, err := getContactByEmailTx(ctx, tx, email)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	now := nowISO()

	if existing == nil {
		paramsJSON := []byte("{}")
		if params != nil {
			paramsJSON, err = json.Marshal(params)
			if err != nil {
				return nil, fmt.Errorf("marshal params: %w", err)
			}
		}
		id := ids.NewContact()
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO contacts (id, email, params, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
			id, email, string(paramsJSON), now, now,
		); err != nil {
			return nil, fmt.Errorf("insert contact: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return s.GetContactByID(ctx, id)
	}

	merged := map[string]any{}
	if existing.Params != "" {
		if err := json.Unmarshal([]byte(existing.Params), &merged); err != nil {
			merged = map[string]any{}
		}
	}
	for k, v := range params {
		merged[k] = v
	}
	mergedJSON, err := json.Marshal(merged)
	if err != nil {
		return nil, fmt.Errorf("marshal merged params: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE contacts SET params = ?, updated_at = ? WHERE id = ?`,
		string(mergedJSON), now, existing.ID,
	); err != nil {
		return nil, fmt.Errorf("update contact: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetContactByID(ctx, existing.ID)
}

// ContactDeletionResult is returned by DeleteContact so callers can
// report how many dependent rows were swept up — useful for audit
// logs when purging hard bouncers.
type ContactDeletionResult struct {
	Contact              *models.Contact
	SubscriptionsRemoved int64
	UnsubEventsRemoved   int64
}

// DeleteContact permanently removes a contact and every row that
// references them (subscriptions + the unsubscribe_events audit log).
// Intended for hard-bounce cleanup so the address cannot be picked
// up by a future bulk send. Use UnsubscribeSubscription for the normal
// opt-out path — that one preserves the suppression record.
//
// Accepts either a contact id (c_...) or a lowercase email; the
// resolution lives inside the same transaction as the deletes so a
// concurrent UpsertContact can't sneak a new row in after we resolved.
func (s *Store) DeleteContact(ctx context.Context, idOrEmail string) (*ContactDeletionResult, error) {
	idOrEmail = strings.TrimSpace(idOrEmail)
	if idOrEmail == "" {
		return nil, errors.New("id or email is required")
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	var contact *models.Contact
	if strings.HasPrefix(idOrEmail, "c_") {
		contact, err = scanContact(tx.QueryRowContext(ctx,
			`SELECT id, email, params, created_at, updated_at FROM contacts WHERE id = ?`, idOrEmail,
		))
	} else {
		contact, err = getContactByEmailTx(ctx, tx, NormalizeEmail(idOrEmail))
	}
	if err != nil {
		return nil, err
	}

	evRes, err := tx.ExecContext(ctx,
		`DELETE FROM unsubscribe_events WHERE contact_id = ?`, contact.ID)
	if err != nil {
		return nil, fmt.Errorf("delete unsubscribe_events: %w", err)
	}
	subRes, err := tx.ExecContext(ctx,
		`DELETE FROM subscriptions WHERE contact_id = ?`, contact.ID)
	if err != nil {
		return nil, fmt.Errorf("delete subscriptions: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM contacts WHERE id = ?`, contact.ID); err != nil {
		return nil, fmt.Errorf("delete contact: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	evCount, _ := evRes.RowsAffected()
	subCount, _ := subRes.RowsAffected()
	return &ContactDeletionResult{
		Contact:              contact,
		SubscriptionsRemoved: subCount,
		UnsubEventsRemoved:   evCount,
	}, nil
}

func (s *Store) GetContactByID(ctx context.Context, id string) (*models.Contact, error) {
	return scanContact(s.DB.QueryRowContext(ctx,
		`SELECT id, email, params, created_at, updated_at FROM contacts WHERE id = ?`, id,
	))
}

func (s *Store) GetContactByEmail(ctx context.Context, email string) (*models.Contact, error) {
	return scanContact(s.DB.QueryRowContext(ctx,
		`SELECT id, email, params, created_at, updated_at FROM contacts WHERE email = ?`, NormalizeEmail(email),
	))
}

func getContactByEmailTx(ctx context.Context, tx *sql.Tx, email string) (*models.Contact, error) {
	return scanContact(tx.QueryRowContext(ctx,
		`SELECT id, email, params, created_at, updated_at FROM contacts WHERE email = ?`, email,
	))
}

func scanContact(row *sql.Row) (*models.Contact, error) {
	var c models.Contact
	var created, updated string
	if err := row.Scan(&c.ID, &c.Email, &c.Params, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var err error
	if c.CreatedAt, err = parseTime(created); err != nil {
		return nil, err
	}
	if c.UpdatedAt, err = parseTime(updated); err != nil {
		return nil, err
	}
	return &c, nil
}
