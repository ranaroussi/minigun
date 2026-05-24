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

func (s *Store) CreateCompany(ctx context.Context, slug, name string) (*models.Company, error) {
	id := ids.NewCompany()
	now := nowISO()
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO companies (id, slug, name, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		id, slug, name, now, now,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrAlreadyExists
		}
		return nil, fmt.Errorf("insert company: %w", err)
	}
	return s.GetCompanyByID(ctx, id)
}

func (s *Store) GetCompanyByID(ctx context.Context, id string) (*models.Company, error) {
	return s.queryCompany(ctx, `SELECT id, slug, name, created_at, updated_at FROM companies WHERE id = ?`, id)
}

func (s *Store) GetCompanyBySlug(ctx context.Context, slug string) (*models.Company, error) {
	return s.queryCompany(ctx, `SELECT id, slug, name, created_at, updated_at FROM companies WHERE slug = ?`, slug)
}

func (s *Store) ResolveCompany(ctx context.Context, idOrSlug string) (*models.Company, error) {
	if c, err := s.GetCompanyByID(ctx, idOrSlug); err == nil {
		return c, nil
	}
	return s.GetCompanyBySlug(ctx, idOrSlug)
}

func (s *Store) queryCompany(ctx context.Context, q string, args ...any) (*models.Company, error) {
	row := s.DB.QueryRowContext(ctx, q, args...)
	var c models.Company
	var created, updated string
	if err := row.Scan(&c.ID, &c.Slug, &c.Name, &created, &updated); err != nil {
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

type CompanySummary struct {
	ID        string    `json:"id"`
	Slug      string    `json:"slug"`
	Name      string    `json:"name"`
	ListCount int       `json:"list_count"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (s *Store) ListCompanies(ctx context.Context) ([]CompanySummary, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT c.id, c.slug, c.name, c.created_at, c.updated_at,
		       COALESCE(COUNT(l.id), 0) AS list_count
		FROM companies c
		LEFT JOIN lists l ON l.company_id = c.id
		GROUP BY c.id
		ORDER BY c.created_at ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CompanySummary
	for rows.Next() {
		var c CompanySummary
		var created, updated string
		if err := rows.Scan(&c.ID, &c.Slug, &c.Name, &created, &updated, &c.ListCount); err != nil {
			return nil, err
		}
		if c.CreatedAt, err = parseTime(created); err != nil {
			return nil, err
		}
		if c.UpdatedAt, err = parseTime(updated); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) ListsForCompany(ctx context.Context, companyID string) ([]models.List, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, slug, name, COALESCE(description, '') AS description,
		       COALESCE(weight, 10) AS weight, COALESCE(company_id, '') AS company_id,
		       created_at, updated_at
		FROM lists
		WHERE company_id = ?
		ORDER BY weight ASC, name ASC`, companyID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.List
	for rows.Next() {
		var l models.List
		var created, updated string
		if err := rows.Scan(&l.ID, &l.Slug, &l.Name, &l.Description, &l.Weight, &l.CompanyID, &created, &updated); err != nil {
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
