package store_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/ranaroussi/minigun/internal/store"
)

// seedHygieneFixture sets up a list with 5 subscribed contacts in
// distinct engagement states, so each prune criterion can be exercised
// against a known truth. Returns the IDs so tests can introspect them.
type hygieneFixture struct {
	ListID         string
	NowMs          int64
	// Five contacts, indexed by personality:
	Fresh          string // subscribed today, no engagement row, NOT prunable on any signal
	HighlyEngaged  string // total_delivered=10, total_opens=5, msgs_since=0
	DormantByCount string // total_delivered=15, total_opens=0, msgs_since=15
	DormantByRec   string // last_engagement_at_ms = 200 days ago
	NeverDelivered string // subscribed 100 days ago, no engagement row
}

func seedHygiene(t *testing.T, db *sql.DB) hygieneFixture {
	t.Helper()
	ctx := context.Background()
	fx := hygieneFixture{
		ListID:         "l_hygiene1",
		NowMs:          time.Now().UnixMilli(),
		Fresh:          "c_fresh",
		HighlyEngaged:  "c_engaged",
		DormantByCount: "c_dormct",
		DormantByRec:   "c_dormrec",
		NeverDelivered: "c_nodel",
	}
	exec := func(q string, args ...any) {
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("seed exec: %v\nQ: %s", err, q)
		}
	}
	exec(`INSERT INTO companies (id, slug, name, sending_domain, created_at, updated_at)
	      VALUES ('co_hyg', 'hyg', 'Hyg', 'mg.example.com', datetime('now'), datetime('now'))`)
	exec(`INSERT INTO lists (id, slug, name, description, weight, company_id, sending_domain, created_at, updated_at)
	      VALUES (?, 'newsletter', 'NL', '', 10, 'co_hyg', 'mg.example.com', datetime('now'), datetime('now'))`, fx.ListID)

	// Build contacts + subscriptions with controlled subscribed_at timestamps.
	type p struct {
		ContactID    string
		Email        string
		SubscribedAt time.Time
	}
	people := []p{
		{fx.Fresh, "fresh@example.com", time.Now().Add(-1 * time.Hour)},
		{fx.HighlyEngaged, "eng@example.com", time.Now().Add(-30 * 24 * time.Hour)},
		{fx.DormantByCount, "dct@example.com", time.Now().Add(-60 * 24 * time.Hour)},
		{fx.DormantByRec, "drec@example.com", time.Now().Add(-300 * 24 * time.Hour)},
		{fx.NeverDelivered, "nodel@example.com", time.Now().Add(-100 * 24 * time.Hour)},
	}
	for _, pp := range people {
		exec(`INSERT INTO contacts (id, email, params, created_at, updated_at)
		      VALUES (?, ?, '{}', datetime('now'), datetime('now'))`, pp.ContactID, pp.Email)
		exec(`INSERT INTO subscriptions (list_id, contact_id, subscribed, subscribed_at, updated_at)
		      VALUES (?, ?, 1, ?, ?)`,
			fx.ListID, pp.ContactID,
			pp.SubscribedAt.UTC().Format(time.RFC3339Nano),
			pp.SubscribedAt.UTC().Format(time.RFC3339Nano))
	}

	// Engagement rows for the three contacts that have one.
	exec(`INSERT INTO contact_engagement
	      (contact_id, list_id, last_delivered_at_ms, last_engagement_at_ms,
	       total_delivered, total_opens, messages_since_last_engagement, updated_at)
	      VALUES (?, ?, ?, ?, 10, 5, 0, datetime('now'))`,
		fx.HighlyEngaged, fx.ListID,
		fx.NowMs-1*24*60*60*1000, fx.NowMs-1*24*60*60*1000)
	exec(`INSERT INTO contact_engagement
	      (contact_id, list_id, last_delivered_at_ms, last_engagement_at_ms,
	       total_delivered, total_opens, messages_since_last_engagement, updated_at)
	      VALUES (?, ?, ?, NULL, 15, 0, 15, datetime('now'))`,
		fx.DormantByCount, fx.ListID, fx.NowMs-2*24*60*60*1000)
	exec(`INSERT INTO contact_engagement
	      (contact_id, list_id, last_delivered_at_ms, last_engagement_at_ms,
	       total_delivered, total_opens, messages_since_last_engagement, updated_at)
	      VALUES (?, ?, ?, ?, 20, 1, 19, datetime('now'))`,
		fx.DormantByRec, fx.ListID,
		fx.NowMs-1*24*60*60*1000, fx.NowMs-200*24*60*60*1000)
	// NeverDelivered intentionally has NO contact_engagement row.

	return fx
}

func TestPruneCandidates_ByCount(t *testing.T) {
	st, d := newTestStore(t)
	fx := seedHygiene(t, d)
	ctx := context.Background()

	cands, err := st.ListPruneCandidates(ctx, store.ListPruneCandidatesParams{
		ListID:   fx.ListID,
		Criteria: store.PruneCriteria{MinMessagesSinceEngagement: 10},
		NowMs:    fx.NowMs,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Expect: DormantByCount (msgs_since=15) and DormantByRec (msgs_since=19).
	if len(cands) != 2 {
		t.Fatalf("want 2 candidates, got %d", len(cands))
	}
	for _, c := range cands {
		if !c.MatchedByCount {
			t.Errorf("contact %s: expected matched_by_count=true", c.ContactID)
		}
		if c.Reason() != "auto-prune-by-count" {
			t.Errorf("contact %s: reason=%s, want auto-prune-by-count", c.ContactID, c.Reason())
		}
	}
}

func TestPruneCandidates_ByRecency(t *testing.T) {
	st, d := newTestStore(t)
	fx := seedHygiene(t, d)
	ctx := context.Background()

	cands, err := st.ListPruneCandidates(ctx, store.ListPruneCandidatesParams{
		ListID: fx.ListID,
		Criteria: store.PruneCriteria{
			DormantForMs: 180 * 24 * 60 * 60 * 1000, // 180 days
		},
		NowMs: fx.NowMs,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Expect: ONLY DormantByRec (last_engagement = 200 days ago).
	// HighlyEngaged is 1 day ago; DormantByCount has NULL last_engagement_at_ms.
	if len(cands) != 1 {
		t.Fatalf("want 1 candidate, got %d", len(cands))
	}
	if cands[0].ContactID != fx.DormantByRec {
		t.Fatalf("want %s, got %s", fx.DormantByRec, cands[0].ContactID)
	}
	if cands[0].Reason() != "auto-prune-by-recency" {
		t.Fatalf("reason: want auto-prune-by-recency, got %s", cands[0].Reason())
	}
}

func TestPruneCandidates_NoDelivery(t *testing.T) {
	st, d := newTestStore(t)
	fx := seedHygiene(t, d)
	ctx := context.Background()

	cands, err := st.ListPruneCandidates(ctx, store.ListPruneCandidatesParams{
		ListID: fx.ListID,
		Criteria: store.PruneCriteria{
			NoDeliveryForMs: 60 * 24 * 60 * 60 * 1000, // 60 days
		},
		NowMs: fx.NowMs,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Expect: NeverDelivered (subscribed 100 days ago, no engagement row).
	// Fresh is subscribed 1h ago — protected by the subscribed_at anchor.
	// HighlyEngaged was delivered 1 day ago — too recent.
	// DormantByRec was delivered 1 day ago — too recent.
	// DormantByCount was delivered 2 days ago — too recent.
	if len(cands) != 1 {
		t.Fatalf("want 1 candidate, got %d", len(cands))
	}
	if cands[0].ContactID != fx.NeverDelivered {
		t.Fatalf("want %s, got %s", fx.NeverDelivered, cands[0].ContactID)
	}
	if cands[0].Reason() != "auto-prune-by-no-delivery" {
		t.Fatalf("reason: got %s", cands[0].Reason())
	}
}

func TestPruneCandidates_Combined_OR(t *testing.T) {
	st, d := newTestStore(t)
	fx := seedHygiene(t, d)
	ctx := context.Background()

	cands, err := st.ListPruneCandidates(ctx, store.ListPruneCandidatesParams{
		ListID: fx.ListID,
		Criteria: store.PruneCriteria{
			MinMessagesSinceEngagement: 10,
			DormantForMs:               180 * 24 * 60 * 60 * 1000,
			NoDeliveryForMs:            60 * 24 * 60 * 60 * 1000,
		},
		NowMs: fx.NowMs,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Expect 3: DormantByCount, DormantByRec, NeverDelivered.
	if len(cands) != 3 {
		t.Fatalf("want 3 candidates, got %d", len(cands))
	}
	// Reason precedence: count > recency > no-delivery.
	// DormantByCount matches count → reason "auto-prune-by-count".
	// DormantByRec matches count (msgs_since=19) AND recency → "auto-prune-by-count".
	// NeverDelivered matches no-delivery only → "auto-prune-by-no-delivery".
	for _, c := range cands {
		switch c.ContactID {
		case fx.DormantByCount, fx.DormantByRec:
			if c.Reason() != "auto-prune-by-count" {
				t.Errorf("%s: reason=%s, want auto-prune-by-count", c.ContactID, c.Reason())
			}
		case fx.NeverDelivered:
			if c.Reason() != "auto-prune-by-no-delivery" {
				t.Errorf("%s: reason=%s, want auto-prune-by-no-delivery", c.ContactID, c.Reason())
			}
		}
	}
}

func TestPruneList_DryRunPreservesData(t *testing.T) {
	st, d := newTestStore(t)
	fx := seedHygiene(t, d)
	ctx := context.Background()

	res, err := st.PruneList(ctx, store.ListPruneCandidatesParams{
		ListID:   fx.ListID,
		Criteria: store.PruneCriteria{MinMessagesSinceEngagement: 10},
		NowMs:    fx.NowMs,
	}, true /* dryRun */, 10)
	if err != nil {
		t.Fatal(err)
	}
	if res.Candidates != 2 {
		t.Fatalf("candidates: want 2, got %d", res.Candidates)
	}
	if res.Unsubscribed != 0 {
		t.Fatalf("unsubscribed: dry-run must be 0, got %d", res.Unsubscribed)
	}
	if !res.DryRun {
		t.Fatal("dry_run flag not set on result")
	}
	// Confirm no subscriptions flipped.
	var subbedCount int
	if err := d.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM subscriptions WHERE list_id = ? AND subscribed = 1`,
		fx.ListID).Scan(&subbedCount); err != nil {
		t.Fatal(err)
	}
	if subbedCount != 5 {
		t.Fatalf("subscriptions: dry-run must preserve all 5, got %d", subbedCount)
	}
	// Confirm no audit rows.
	var auditCount int
	if err := d.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM unsubscribe_events WHERE list_id = ?`, fx.ListID).
		Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 0 {
		t.Fatalf("audit: dry-run must write 0 rows, got %d", auditCount)
	}
}

func TestPruneList_RealRunUnsubscribesAndAudits(t *testing.T) {
	st, d := newTestStore(t)
	fx := seedHygiene(t, d)
	ctx := context.Background()

	res, err := st.PruneList(ctx, store.ListPruneCandidatesParams{
		ListID:   fx.ListID,
		Criteria: store.PruneCriteria{MinMessagesSinceEngagement: 10},
		NowMs:    fx.NowMs,
	}, false /* dryRun */, 10)
	if err != nil {
		t.Fatal(err)
	}
	if res.Unsubscribed != 2 {
		t.Fatalf("unsubscribed: want 2, got %d", res.Unsubscribed)
	}
	if res.DryRun {
		t.Fatal("dry_run flag set on real-run result")
	}
	// Confirm subscriptions flipped to 0.
	var subbedCount int
	if err := d.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM subscriptions WHERE list_id = ? AND subscribed = 1`,
		fx.ListID).Scan(&subbedCount); err != nil {
		t.Fatal(err)
	}
	if subbedCount != 3 {
		t.Fatalf("subscriptions: 2 should be unsubscribed (3 remain), got %d remaining", subbedCount)
	}
	// Confirm audit rows with reason.
	rows, err := d.QueryContext(ctx, `
		SELECT contact_id, reason FROM unsubscribe_events
		WHERE list_id = ? ORDER BY contact_id`, fx.ListID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var auditRows int
	for rows.Next() {
		var contactID string
		var reason sql.NullString
		if err := rows.Scan(&contactID, &reason); err != nil {
			t.Fatal(err)
		}
		auditRows++
		if !reason.Valid || reason.String != "auto-prune-by-count" {
			t.Errorf("audit row %s: reason=%+v, want auto-prune-by-count", contactID, reason)
		}
	}
	if auditRows != 2 {
		t.Fatalf("audit: want 2 rows, got %d", auditRows)
	}
}

func TestPruneList_Idempotent(t *testing.T) {
	st, d := newTestStore(t)
	fx := seedHygiene(t, d)
	ctx := context.Background()

	first, err := st.PruneList(ctx, store.ListPruneCandidatesParams{
		ListID:   fx.ListID,
		Criteria: store.PruneCriteria{MinMessagesSinceEngagement: 10},
		NowMs:    fx.NowMs,
	}, false, 10)
	if err != nil {
		t.Fatal(err)
	}
	if first.Unsubscribed != 2 {
		t.Fatalf("first run: want 2, got %d", first.Unsubscribed)
	}
	second, err := st.PruneList(ctx, store.ListPruneCandidatesParams{
		ListID:   fx.ListID,
		Criteria: store.PruneCriteria{MinMessagesSinceEngagement: 10},
		NowMs:    fx.NowMs,
	}, false, 10)
	if err != nil {
		t.Fatal(err)
	}
	if second.Candidates != 0 {
		t.Fatalf("second run: want 0 candidates (idempotent), got %d", second.Candidates)
	}
	if second.Unsubscribed != 0 {
		t.Fatalf("second run: want 0 unsubscribed, got %d", second.Unsubscribed)
	}
}

func TestPruneCandidates_RequiresAtLeastOneCriterion(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()
	_, err := st.ListPruneCandidates(ctx, store.ListPruneCandidatesParams{
		ListID:   "l_hygiene1",
		Criteria: store.PruneCriteria{},
	})
	if err == nil {
		t.Fatal("expected error when no criterion is set")
	}
}

func TestPruneList_RespectsLimit(t *testing.T) {
	st, d := newTestStore(t)
	fx := seedHygiene(t, d)
	ctx := context.Background()
	res, err := st.PruneList(ctx, store.ListPruneCandidatesParams{
		ListID:   fx.ListID,
		Criteria: store.PruneCriteria{MinMessagesSinceEngagement: 10},
		NowMs:    fx.NowMs,
		Limit:    1,
	}, false, 10)
	if err != nil {
		t.Fatal(err)
	}
	if res.Unsubscribed != 1 {
		t.Fatalf("limit=1: want 1 unsubscribed, got %d", res.Unsubscribed)
	}
	// Confirm exactly 1 subscription flipped, leaving 4 still subscribed.
	var subbedCount int
	if err := d.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM subscriptions WHERE list_id = ? AND subscribed = 1`,
		fx.ListID).Scan(&subbedCount); err != nil {
		t.Fatal(err)
	}
	if subbedCount != 4 {
		t.Fatalf("want 4 remaining subscribed, got %d", subbedCount)
	}
}
