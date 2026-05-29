package worker

import (
	"database/sql"
	"testing"
	"time"

	"github.com/ranaroussi/minigun/internal/store"
)

const (
	testHourMS = int64(60 * 60 * 1000)
	testDayMS  = 24 * testHourMS
)

func TestNextEventPullDueAt_BurstPhase(t *testing.T) {
	created := int64(1_700_000_000_000)
	cases := []struct {
		name        string
		pulls       int64
		lastPulled  sql.NullInt64
		wantDueAt   int64
		wantFrozen  bool
	}{
		{"pull 0 — first beat at created_at", 0, sql.NullInt64{}, created, false},
		{"pull 1 — +1h", 1, sql.NullInt64{Int64: created, Valid: true}, created + 1*testHourMS, false},
		{"pull 2 — +6h", 2, sql.NullInt64{Int64: created + 1*testHourMS, Valid: true}, created + 6*testHourMS, false},
		{"pull 3 — +24h", 3, sql.NullInt64{Int64: created + 6*testHourMS, Valid: true}, created + 24*testHourMS, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			row := store.DueEventPullRow{
				CreatedAtMs:          created,
				EventsPullsCount:     tc.pulls,
				EventsLastPulledAtMs: tc.lastPulled,
			}
			got, frozen := NextEventPullDueAt(row)
			if got != tc.wantDueAt {
				t.Fatalf("dueAt: want %d, got %d", tc.wantDueAt, got)
			}
			if frozen != tc.wantFrozen {
				t.Fatalf("frozen: want %v, got %v", tc.wantFrozen, frozen)
			}
		})
	}
}

func TestNextEventPullDueAt_DailyPhase(t *testing.T) {
	created := int64(1_700_000_000_000)
	last := created + 25*testHourMS
	row := store.DueEventPullRow{
		CreatedAtMs:          created,
		EventsPullsCount:     4,
		EventsLastPulledAtMs: sql.NullInt64{Int64: last, Valid: true},
	}
	got, frozen := NextEventPullDueAt(row)
	if got != last+testDayMS {
		t.Fatalf("dueAt: want %d, got %d", last+testDayMS, got)
	}
	if frozen {
		t.Fatal("frozen unexpectedly true")
	}
}

func TestNextEventPullDueAt_FrozenAfterWindow(t *testing.T) {
	created := int64(1_700_000_000_000)
	last := created + ArchiveMaxAgeMS - 1*testHourMS
	row := store.DueEventPullRow{
		CreatedAtMs:          created,
		EventsPullsCount:     20,
		EventsLastPulledAtMs: sql.NullInt64{Int64: last, Valid: true},
	}
	_, frozen := NextEventPullDueAt(row)
	if !frozen {
		t.Fatal("expected frozen=true past the archive window")
	}
}

func TestNextEventPullDueAt_DailyPhaseFallbackToNow(t *testing.T) {
	created := int64(1_700_000_000_000)
	row := store.DueEventPullRow{
		CreatedAtMs:          created,
		EventsPullsCount:     4,
		EventsLastPulledAtMs: sql.NullInt64{}, // shouldn't happen, but tested defensively
	}
	before := time.Now().UnixMilli()
	got, frozen := NextEventPullDueAt(row)
	after := time.Now().UnixMilli()
	if frozen {
		t.Fatal("frozen unexpectedly true")
	}
	if got < before || got > after+10 {
		t.Fatalf("dueAt: expected ~now (%d..%d), got %d", before, after, got)
	}
}
