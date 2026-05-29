package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
)

// ---------------------------------------------------------------------------
// worker_state — tiny key/value sentinel store
// ---------------------------------------------------------------------------
//
// Persistent state for cron throttles ("run at most once per day"-style
// invariants that need to survive process restarts and Cloudflare Worker
// invocation boundaries). Intentionally minimal: TEXT values + updated_at.
// Callers serialize whatever they want into the value.
//
// Conventions for well-known keys:
//   auto_prune_last_run_ms — epoch-ms timestamp of the last successful
//                            auto-prune sweep. Used by the auto-prune
//                            scheduler to refuse to re-run within 24h.

// GetState returns the value stored at key, or ("", false, nil) if absent.
func (s *Store) GetState(ctx context.Context, key string) (string, bool, error) {
	var v string
	err := s.DB.QueryRowContext(ctx, `SELECT value FROM worker_state WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

// SetState upserts (key, value, updated_at=now). Idempotent.
func (s *Store) SetState(ctx context.Context, key, value string) error {
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO worker_state (key, value, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET
		  value      = excluded.value,
		  updated_at = excluded.updated_at`,
		key, value, nowISO(),
	)
	return err
}

// GetStateInt64 is a convenience wrapper that parses the value as int64.
// Returns (0, false, nil) for missing keys; (0, true, err) for parse
// errors so the caller can distinguish "never set" from "garbage."
func (s *Store) GetStateInt64(ctx context.Context, key string) (int64, bool, error) {
	v, ok, err := s.GetState(ctx, key)
	if err != nil || !ok {
		return 0, ok, err
	}
	n, parseErr := strconv.ParseInt(v, 10, 64)
	if parseErr != nil {
		return 0, true, parseErr
	}
	return n, true, nil
}

// SetStateInt64 serializes n as a decimal string.
func (s *Store) SetStateInt64(ctx context.Context, key string, n int64) error {
	return s.SetState(ctx, key, strconv.FormatInt(n, 10))
}
