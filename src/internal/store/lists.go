package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/ranaroussi/minigun/internal/ids"
	"github.com/ranaroussi/minigun/internal/models"
)

var ErrNotFound = errors.New("not found")
var ErrAlreadyExists = errors.New("already exists")

type NewListParams struct {
	Slug          string
	Name          string
	CompanyID     string
	SendingDomain string
	Description   string
	Weight        int
}

func (s *Store) CreateList(ctx context.Context, p NewListParams) (*models.List, error) {
	id := ids.NewList()
	now := nowISO()
	if p.Weight == 0 {
		p.Weight = 10
	}
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO lists (id, slug, name, description, weight, company_id, sending_domain, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, p.Slug, p.Name, p.Description, p.Weight, p.CompanyID, p.SendingDomain, now, now,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrAlreadyExists
		}
		return nil, fmt.Errorf("insert list: %w", err)
	}
	return s.GetListByID(ctx, id)
}

const listSelect = `SELECT id, slug, name, COALESCE(description, '') AS description,
		COALESCE(weight, 10) AS weight, COALESCE(company_id, '') AS company_id,
		sending_domain, created_at, updated_at FROM lists`

func (s *Store) GetListByID(ctx context.Context, id string) (*models.List, error) {
	return s.queryList(ctx, listSelect+` WHERE id = ?`, id)
}

func (s *Store) GetListBySlug(ctx context.Context, slug string) (*models.List, error) {
	return s.queryList(ctx, listSelect+` WHERE slug = ?`, slug)
}

func (s *Store) ResolveList(ctx context.Context, idOrSlug string) (*models.List, error) {
	if l, err := s.GetListByID(ctx, idOrSlug); err == nil {
		return l, nil
	}
	return s.GetListBySlug(ctx, idOrSlug)
}

func (s *Store) queryList(ctx context.Context, q string, args ...any) (*models.List, error) {
	row := s.DB.QueryRowContext(ctx, q, args...)
	var l models.List
	var created, updated string
	if err := row.Scan(&l.ID, &l.Slug, &l.Name, &l.Description, &l.Weight, &l.CompanyID, &l.SendingDomain, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var err error
	if l.CreatedAt, err = parseTime(created); err != nil {
		return nil, err
	}
	if l.UpdatedAt, err = parseTime(updated); err != nil {
		return nil, err
	}
	return &l, nil
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return contains(msg, "UNIQUE constraint failed") || contains(msg, "constraint failed: UNIQUE")
}

func contains(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
