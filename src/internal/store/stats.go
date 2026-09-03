package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/ranaroussi/minigun/internal/models"
)

func (s *Store) GetSendStats(ctx context.Context, sendID string) (*models.SendStats, error) {
	row := s.DB.QueryRowContext(ctx, `
		SELECT send_id, sent, delivered, opened, clicked, failed, complained, unsubscribed,
		       first_fetched_at, last_fetched_at, next_fetch_at, is_final, fetch_error,
		       created_at, updated_at
		FROM send_stats WHERE send_id = ?`, sendID)
	return scanSendStats(row)
}

func scanSendStats(row *sql.Row) (*models.SendStats, error) {
	var st models.SendStats
	var firstFetched, lastFetched, nextFetch, fetchErr sql.NullString
	var createdAt, updatedAt string
	var isFinalInt int
	if err := row.Scan(
		&st.SendID, &st.Sent, &st.Delivered, &st.Opened, &st.Clicked, &st.Failed, &st.Complained, &st.Unsubscribed,
		&firstFetched, &lastFetched, &nextFetch, &isFinalInt, &fetchErr,
		&createdAt, &updatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	st.IsFinal = isFinalInt == 1
	st.FetchError = stringPtr(fetchErr)
	var err error
	if st.FirstFetchedAt, err = parseTimePtr(firstFetched); err != nil {
		return nil, err
	}
	if st.LastFetchedAt, err = parseTimePtr(lastFetched); err != nil {
		return nil, err
	}
	if st.NextFetchAt, err = parseTimePtr(nextFetch); err != nil {
		return nil, err
	}
	if st.CreatedAt, err = parseTime(createdAt); err != nil {
		return nil, err
	}
	if st.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return nil, err
	}
	return &st, nil
}

func (s *Store) initSendStatsRow(ctx context.Context, tx *sql.Tx, sendID string) error {
	_, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO send_stats (send_id) VALUES (?)`, sendID,
	)
	return err
}

type SendStatsUpdate struct {
	Sent       uint64
	Delivered  uint64
	Opened     uint64
	Clicked    uint64
	Failed     uint64
	Complained uint64
	NextFetch  *time.Time
	IsFinal    bool
	FetchError *string
}

func (s *Store) RecordStatsFetchError(ctx context.Context, sendID string, nextFetch *time.Time, isFinal bool, errMsg string) error {
	now := nowISO()
	var nextFetchVal any
	if nextFetch != nil && !isFinal {
		nextFetchVal = nextFetch.UTC().Format(time.RFC3339Nano)
	}
	isFinalInt := 0
	if isFinal {
		isFinalInt = 1
	}
	_, err := s.DB.ExecContext(ctx, `
		UPDATE send_stats SET
		  last_fetched_at = ?,
		  next_fetch_at = ?,
		  is_final = ?,
		  fetch_error = ?,
		  updated_at = ?
		WHERE send_id = ?`,
		now, nextFetchVal, isFinalInt, errMsg, now, sendID,
	)
	return err
}

func (s *Store) ApplyMailgunStats(ctx context.Context, sendID string, u SendStatsUpdate) error {
	now := nowISO()
	var nextFetch any
	if u.NextFetch != nil && !u.IsFinal {
		nextFetch = u.NextFetch.UTC().Format(time.RFC3339Nano)
	}
	isFinalInt := 0
	if u.IsFinal {
		isFinalInt = 1
	}
	_, err := s.DB.ExecContext(ctx, `
		UPDATE send_stats SET
		  sent = ?, delivered = ?, opened = ?, clicked = ?, failed = ?, complained = ?,
		  first_fetched_at = COALESCE(first_fetched_at, ?),
		  last_fetched_at = ?,
		  next_fetch_at = ?,
		  is_final = ?,
		  fetch_error = ?,
		  updated_at = ?
		WHERE send_id = ?`,
		u.Sent, u.Delivered, u.Opened, u.Clicked, u.Failed, u.Complained,
		now, now, nextFetch, isFinalInt, nullString(u.FetchError), now,
		sendID,
	)
	return err
}

func (s *Store) IncrementSendStatsUnsubscribed(ctx context.Context, tx *sql.Tx, sendID string) error {
	if sendID == "" {
		return nil
	}
	exec := func(query string, args ...any) error {
		if tx != nil {
			_, err := tx.ExecContext(ctx, query, args...)
			return err
		}
		_, err := s.DB.ExecContext(ctx, query, args...)
		return err
	}
	return exec(`
		INSERT INTO send_stats (send_id, unsubscribed, updated_at)
		VALUES (?, 1, datetime('now'))
		ON CONFLICT(send_id) DO UPDATE SET
		  unsubscribed = unsubscribed + 1,
		  updated_at = datetime('now')`,
		sendID,
	)
}

func (s *Store) MarkSendCompletedForStats(ctx context.Context, sendID string) error {
	_, err := s.DB.ExecContext(ctx, `
		UPDATE send_stats SET next_fetch_at = datetime('now'), updated_at = datetime('now')
		WHERE send_id = ? AND next_fetch_at IS NULL AND is_final = 0`, sendID,
	)
	return err
}

type DueStatsRow struct {
	SendID      string
	CreatedAt   time.Time
	CompletedAt time.Time
}

func (s *Store) ListDueSendStats(ctx context.Context, limit int) ([]DueStatsRow, error) {
	if limit <= 0 {
		limit = 50
	}
	// next_fetch_at is written in two formats: ApplyMailgunStats stores
	// RFC3339Nano ("2026-06-05T12:09:15.905Z") while MarkSendCompletedForStats
	// stores SQLite's datetime() ("2026-06-05 12:09:15"). A raw string "<="
	// compares the 'T' separator (0x54) against ' ' (0x20), so an RFC3339
	// timestamp always sorts AFTER a same-day SQLite "now" and the row never
	// reads as due until the next calendar day. Normalize both sides through
	// datetime() so the comparison is chronological.
	rows, err := s.DB.QueryContext(ctx, `
		SELECT s.id, s.created_at, s.completed_at
		FROM send_stats ss
		JOIN sends s ON s.id = ss.send_id
		WHERE ss.is_final = 0
		  AND ss.next_fetch_at IS NOT NULL
		  AND datetime(ss.next_fetch_at) <= datetime('now')
		  AND s.completed_at IS NOT NULL
		ORDER BY datetime(ss.next_fetch_at) ASC
		LIMIT ?`, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DueStatsRow
	for rows.Next() {
		var r DueStatsRow
		var created, completed string
		if err := rows.Scan(&r.SendID, &created, &completed); err != nil {
			return nil, err
		}
		if r.CreatedAt, err = parseTime(created); err != nil {
			return nil, err
		}
		if r.CompletedAt, err = parseTime(completed); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
