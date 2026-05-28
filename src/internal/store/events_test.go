package store_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/ranaroussi/minigun/internal/db"
	"github.com/ranaroussi/minigun/internal/mailgun"
	"github.com/ranaroussi/minigun/internal/store"
)

// newTestStore spins up an on-disk SQLite (modernc.org/sqlite is a CGo-less
// pure-Go driver) under t.TempDir + runs the real migrations. We use disk
// rather than :memory: because goose holds its own connection state; disk
// is the cheapest reliable option and t.TempDir cleans up automatically.
func newTestStore(t *testing.T) (*store.Store, *sql.DB) {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "minigun.test.db")
	d, err := db.Open(ctx, path)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Migrate(ctx, d); err != nil {
		_ = d.Close()
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return store.New(d), d
}

// seedContext seeds the minimum graph needed for the read-side tests:
// one company, one list, one contact, one send (status=completed). Returns
// the identifiers so tests can reference them.
type seedIDs struct {
	CompanyID  string
	ListID     string
	ContactID  string
	ContactEM  string
	SendID     string
	Domain     string
	CreatedAt  time.Time
}

func seed(t *testing.T, db *sql.DB) seedIDs {
	t.Helper()
	ctx := context.Background()
	ids := seedIDs{
		CompanyID: "co_test123456",
		ListID:    "l_test123456",
		ContactID: "c_test123456",
		ContactEM: "alice@example.com",
		SendID:    "s_test123456",
		Domain:    "mg.example.com",
		CreatedAt: time.Now().UTC().Add(-2 * time.Hour),
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO companies (id, slug, name, sending_domain, created_at, updated_at)
		VALUES (?, 'test', 'Test', ?, datetime('now'), datetime('now'))`,
		ids.CompanyID, ids.Domain); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO lists (id, slug, name, description, weight, company_id, sending_domain, created_at, updated_at)
		VALUES (?, 'newsletter', 'Newsletter', '', 10, ?, ?, datetime('now'), datetime('now'))`,
		ids.ListID, ids.CompanyID, ids.Domain); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO contacts (id, email, params, created_at, updated_at)
		VALUES (?, ?, '{}', datetime('now'), datetime('now'))`,
		ids.ContactID, ids.ContactEM); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO sends (
		  id, type, list_id, subject, from_header, sending_domain, status,
		  batch_size, throttle_ms, test_mode, last_subscription_id,
		  total_recipients, unsubscribe_mode, created_at, updated_at, completed_at
		) VALUES (?, 'bulk', ?, 'Hi', 'Ran <r@x.com>', ?, 'completed',
		          500, 1000, 0, 0,
		          100, 'local', ?, datetime('now'), datetime('now'))`,
		ids.SendID, ids.ListID, ids.Domain, ids.CreatedAt.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	return ids
}

func TestInsertEventIfNew_Idempotency(t *testing.T) {
	st, d := newTestStore(t)
	ids := seed(t, d)
	ctx := context.Background()

	ev, ok := store.NormalizeEvent(mailgun.RawEvent{
		ID:        "mg_event_001",
		Event:     "delivered",
		Timestamp: float64(ids.CreatedAt.Unix()),
		Recipient: ids.ContactEM,
		Tags:      []string{ids.SendID},
	}, ids.Domain, ids.SendID)
	if !ok {
		t.Fatal("normalize: expected ok")
	}

	first, err := st.InsertEventIfNew(ctx, ev)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Inserted {
		t.Fatal("expected first insert to be new")
	}
	if !first.ContactID.Valid || first.ContactID.String != ids.ContactID {
		t.Fatalf("contact_id not resolved: got %+v", first.ContactID)
	}

	second, err := st.InsertEventIfNew(ctx, ev)
	if err != nil {
		t.Fatal(err)
	}
	if second.Inserted {
		t.Fatal("expected second insert to be a dup (no-op)")
	}
}

func TestApplyEventToEngagement_OpenResetsDormancy(t *testing.T) {
	st, d := newTestStore(t)
	ids := seed(t, d)
	ctx := context.Background()

	tsMs := ids.CreatedAt.UnixMilli()
	if err := st.ApplyEventToEngagement(ctx, ids.ContactID, ids.ListID, "delivered", tsMs); err != nil {
		t.Fatal(err)
	}
	if err := st.ApplyEventToEngagement(ctx, ids.ContactID, ids.ListID, "delivered", tsMs+1000); err != nil {
		t.Fatal(err)
	}
	if err := st.ApplyEventToEngagement(ctx, ids.ContactID, ids.ListID, "delivered", tsMs+2000); err != nil {
		t.Fatal(err)
	}

	rows, err := st.ListContactEngagement(ctx, ids.ContactID, ids.ListID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 engagement row, got %d", len(rows))
	}
	if rows[0].TotalDelivered != 3 {
		t.Fatalf("total_delivered: want 3, got %d", rows[0].TotalDelivered)
	}
	if rows[0].MessagesSinceLastEngagement != 3 {
		t.Fatalf("messages_since_last_engagement: want 3, got %d", rows[0].MessagesSinceLastEngagement)
	}

	if err := st.ApplyEventToEngagement(ctx, ids.ContactID, ids.ListID, "opened", tsMs+3000); err != nil {
		t.Fatal(err)
	}
	rows, err = st.ListContactEngagement(ctx, ids.ContactID, ids.ListID)
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].TotalOpens != 1 {
		t.Fatalf("total_opens: want 1, got %d", rows[0].TotalOpens)
	}
	if rows[0].MessagesSinceLastEngagement != 0 {
		t.Fatalf("messages_since_last_engagement should reset to 0, got %d", rows[0].MessagesSinceLastEngagement)
	}
	if !rows[0].LastEngagementAtMs.Valid || rows[0].LastEngagementAtMs.Int64 != tsMs+3000 {
		t.Fatalf("last_engagement_at_ms: want %d, got %+v", tsMs+3000, rows[0].LastEngagementAtMs)
	}
}

func TestApplyEventToEngagement_OutOfOrderDoesntRewind(t *testing.T) {
	st, d := newTestStore(t)
	ids := seed(t, d)
	ctx := context.Background()

	tsMs := ids.CreatedAt.UnixMilli()
	if err := st.ApplyEventToEngagement(ctx, ids.ContactID, ids.ListID, "opened", tsMs+10000); err != nil {
		t.Fatal(err)
	}
	// Older event arriving later shouldn't move last_open_at_ms backwards.
	if err := st.ApplyEventToEngagement(ctx, ids.ContactID, ids.ListID, "opened", tsMs+1000); err != nil {
		t.Fatal(err)
	}
	rows, err := st.ListContactEngagement(ctx, ids.ContactID, ids.ListID)
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].LastOpenAtMs.Int64 != tsMs+10000 {
		t.Fatalf("last_open_at_ms got rewound: want %d, got %d", tsMs+10000, rows[0].LastOpenAtMs.Int64)
	}
	if rows[0].TotalOpens != 2 {
		t.Fatalf("total_opens: want 2 (both events counted), got %d", rows[0].TotalOpens)
	}
}

func TestListSendEvents_PaginationAndFilter(t *testing.T) {
	st, d := newTestStore(t)
	ids := seed(t, d)
	ctx := context.Background()

	// Seed 5 events: 3 delivered, 2 opened, monotonically increasing ts.
	baseMs := ids.CreatedAt.UnixMilli()
	events := []struct {
		id    string
		event string
		ts    int64
	}{
		{"mg_e1", "delivered", baseMs + 1000},
		{"mg_e2", "delivered", baseMs + 2000},
		{"mg_e3", "opened", baseMs + 3000},
		{"mg_e4", "delivered", baseMs + 4000},
		{"mg_e5", "opened", baseMs + 5000},
	}
	for _, e := range events {
		ev, ok := store.NormalizeEvent(mailgun.RawEvent{
			ID:        e.id,
			Event:     e.event,
			Timestamp: float64(e.ts) / 1000.0,
			Recipient: ids.ContactEM,
			Tags:      []string{ids.SendID},
		}, ids.Domain, ids.SendID)
		if !ok {
			t.Fatal("normalize")
		}
		if _, err := st.InsertEventIfNew(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}

	// Full listing — should return 5 in chronological order.
	all, err := st.ListSendEvents(ctx, store.ListSendEventsParams{
		SendID: ids.SendID,
		Limit:  10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 5 {
		t.Fatalf("want 5 events, got %d", len(all))
	}
	for i := 1; i < len(all); i++ {
		if all[i].EventTimestampMs < all[i-1].EventTimestampMs {
			t.Fatalf("events out of order at %d", i)
		}
	}

	// Event filter.
	opens, err := st.ListSendEvents(ctx, store.ListSendEventsParams{
		SendID:    ids.SendID,
		EventType: "opened",
		Limit:     10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(opens) != 2 {
		t.Fatalf("want 2 opens, got %d", len(opens))
	}

	// Pagination — limit 2 should yield first 2.
	page1, err := st.ListSendEvents(ctx, store.ListSendEventsParams{
		SendID: ids.SendID,
		Limit:  2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page1) != 2 {
		t.Fatalf("page1: want 2, got %d", len(page1))
	}
	page2, err := st.ListSendEvents(ctx, store.ListSendEventsParams{
		SendID:    ids.SendID,
		AfterTsMs: page1[1].EventTimestampMs,
		AfterID:   page1[1].ID,
		Limit:     10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page2) != 3 {
		t.Fatalf("page2: want 3, got %d", len(page2))
	}
	if page2[0].MailgunEventID == page1[1].MailgunEventID {
		t.Fatal("page2 must not include the cursor boundary row")
	}

	// SinceMs filter.
	since, err := st.ListSendEvents(ctx, store.ListSendEventsParams{
		SendID:  ids.SendID,
		SinceMs: baseMs + 3500,
		Limit:   10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(since) != 2 {
		t.Fatalf("since: want 2, got %d", len(since))
	}
}

func TestResolveContactID(t *testing.T) {
	st, d := newTestStore(t)
	ids := seed(t, d)
	ctx := context.Background()

	byID, err := st.ResolveContactID(ctx, ids.ContactID)
	if err != nil {
		t.Fatal(err)
	}
	if byID != ids.ContactID {
		t.Fatalf("by id: want %s got %s", ids.ContactID, byID)
	}

	byEmail, err := st.ResolveContactID(ctx, ids.ContactEM)
	if err != nil {
		t.Fatal(err)
	}
	if byEmail != ids.ContactID {
		t.Fatalf("by email: want %s got %s", ids.ContactID, byEmail)
	}

	if _, err := st.ResolveContactID(ctx, "nope@nowhere"); err == nil {
		t.Fatal("expected error for missing contact")
	}
}

// ---------------------------------------------------------------------------
// Phase 5 hardening tests
// ---------------------------------------------------------------------------

// M5: a `delivered` event with a timestamp older than the contact's
// last engagement must NOT increment messages_since_last_engagement.
// Otherwise late-arriving delivered-for-already-opened messages would
// falsely inflate dormancy and bias prune-by-count toward false positives.
func TestApplyEventToEngagement_DeliveredOutOfOrderDoesntInflateDormancy(t *testing.T) {
	st, d := newTestStore(t)
	ids := seed(t, d)
	ctx := context.Background()

	tsMs := ids.CreatedAt.UnixMilli()
	// Establish engagement at T=10000.
	if err := st.ApplyEventToEngagement(ctx, ids.ContactID, ids.ListID, "delivered", tsMs+1000); err != nil {
		t.Fatal(err)
	}
	if err := st.ApplyEventToEngagement(ctx, ids.ContactID, ids.ListID, "opened", tsMs+10000); err != nil {
		t.Fatal(err)
	}

	before, err := st.ListContactEngagement(ctx, ids.ContactID, ids.ListID)
	if err != nil {
		t.Fatal(err)
	}
	if before[0].MessagesSinceLastEngagement != 0 {
		t.Fatalf("baseline msgs_since_eng: want 0 after open, got %d", before[0].MessagesSinceLastEngagement)
	}

	// Late delivered at T=5000 (older than the open at T=10000). Must
	// bump total_delivered but NOT msgs_since_engagement.
	if err := st.ApplyEventToEngagement(ctx, ids.ContactID, ids.ListID, "delivered", tsMs+5000); err != nil {
		t.Fatal(err)
	}
	after, err := st.ListContactEngagement(ctx, ids.ContactID, ids.ListID)
	if err != nil {
		t.Fatal(err)
	}
	if after[0].TotalDelivered != 2 {
		t.Fatalf("total_delivered: want 2 (both events counted), got %d", after[0].TotalDelivered)
	}
	if after[0].MessagesSinceLastEngagement != 0 {
		t.Fatalf("msgs_since_eng: want 0 (older delivered should not inflate), got %d", after[0].MessagesSinceLastEngagement)
	}

	// Sanity: a fresh delivered AFTER the open DOES bump the counter.
	if err := st.ApplyEventToEngagement(ctx, ids.ContactID, ids.ListID, "delivered", tsMs+15000); err != nil {
		t.Fatal(err)
	}
	final, err := st.ListContactEngagement(ctx, ids.ContactID, ids.ListID)
	if err != nil {
		t.Fatal(err)
	}
	if final[0].MessagesSinceLastEngagement != 1 {
		t.Fatalf("msgs_since_eng after fresh delivered: want 1, got %d", final[0].MessagesSinceLastEngagement)
	}
}

// M6: events insert with engagement_applied = 0; MarkEngagementApplied
// flips to 1; ListUnappliedEngagementEvents returns only un-applied rows.
// The replay scanner relies on this contract.
func TestEngagementAppliedFlag_RoundTrip(t *testing.T) {
	st, d := newTestStore(t)
	ids := seed(t, d)
	ctx := context.Background()

	ev, ok := store.NormalizeEvent(mailgun.RawEvent{
		ID:        "mg_e_phase5_1",
		Event:     "delivered",
		Timestamp: float64(ids.CreatedAt.Unix() + 10),
		Recipient: ids.ContactEM,
		Tags:      []string{ids.SendID},
	}, ids.Domain, ids.SendID)
	if !ok {
		t.Fatal("normalize")
	}
	res, err := st.InsertEventIfNew(ctx, ev)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Inserted || res.ID == "" {
		t.Fatalf("insert: want Inserted=true with non-empty ID, got %+v", res)
	}

	// Brand-new row should appear in the un-applied queue.
	pending, err := st.ListUnappliedEngagementEvents(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending: want 1, got %d", len(pending))
	}
	if pending[0].ID != res.ID {
		t.Fatalf("pending id mismatch: want %s, got %s", res.ID, pending[0].ID)
	}

	// Marking it applied removes it from the queue.
	if err := st.MarkEngagementApplied(ctx, res.ID); err != nil {
		t.Fatal(err)
	}
	pending, err = st.ListUnappliedEngagementEvents(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending after mark: want 0, got %d", len(pending))
	}
}

// H3: ListDueEventPulls must include sends past the archive window so
// the worker layer can run a final pull and freeze them. The Phase 2
// SQL age filter excluded them and left events_archive_complete=0 forever.
func TestListDueEventPulls_IncludesPastWindow(t *testing.T) {
	st, d := newTestStore(t)
	ctx := context.Background()
	// Seed a fresh-ish send and an extremely old send. Both must appear
	// in the candidate set so the worker layer can decide what to do.
	if _, err := d.ExecContext(ctx, `
		INSERT INTO companies (id, slug, name, sending_domain, created_at, updated_at)
		VALUES ('co_phase5', 'phase5', 'Phase 5', 'mg.x.com', datetime('now'), datetime('now'))`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecContext(ctx, `
		INSERT INTO sends (
		  id, type, subject, from_header, sending_domain, status,
		  batch_size, throttle_ms, test_mode, last_subscription_id,
		  total_recipients, unsubscribe_mode, created_at, updated_at, completed_at
		) VALUES ('s_old', 'bulk', 'Old', 'r@x.com', 'mg.x.com', 'completed',
		          500, 1000, 0, 0, 100, 'local',
		          datetime('now', '-90 days'), datetime('now', '-90 days'), datetime('now', '-90 days'))`,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecContext(ctx, `
		INSERT INTO sends (
		  id, type, subject, from_header, sending_domain, status,
		  batch_size, throttle_ms, test_mode, last_subscription_id,
		  total_recipients, unsubscribe_mode, created_at, updated_at, completed_at
		) VALUES ('s_new', 'bulk', 'New', 'r@x.com', 'mg.x.com', 'completed',
		          500, 1000, 0, 0, 100, 'local',
		          datetime('now', '-1 hour'), datetime('now', '-1 hour'), datetime('now', '-1 hour'))`,
	); err != nil {
		t.Fatal(err)
	}
	rows, err := st.ListDueEventPulls(ctx, time.Now().UnixMilli(), 30*24*60*60*1000, 100)
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, r := range rows {
		have[r.SendID] = true
	}
	if !have["s_new"] {
		t.Fatal("missing recent send s_new from candidates")
	}
	if !have["s_old"] {
		t.Fatal("missing past-window send s_old — would never get frozen")
	}
}

// H4: PruneList's apply step must be atomic — unsubscribe + audit row
// in the same tx. Verifies that an apply leaves both pieces of state
// consistent (and that re-running yields zero candidates, i.e.
// idempotent).
func TestPruneList_AtomicUnsubscribeAndAudit(t *testing.T) {
	st, d := newTestStore(t)
	ids := seed(t, d)
	ctx := context.Background()

	// Subscribe the contact and bump dormancy beyond threshold.
	subAt := time.Now().UTC().Add(-200 * 24 * time.Hour).Format(time.RFC3339Nano)
	if _, err := d.ExecContext(ctx, `
		INSERT INTO subscriptions (list_id, contact_id, subscribed, subscribed_at, updated_at)
		VALUES (?, ?, 1, ?, ?)`,
		ids.ListID, ids.ContactID, subAt, subAt); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecContext(ctx, `
		INSERT INTO contact_engagement
		  (contact_id, list_id, last_delivered_at_ms,
		   total_delivered, total_opens, total_clicks,
		   messages_since_last_engagement, updated_at)
		VALUES (?, ?, ?, 50, 0, 0, 50, datetime('now'))`,
		ids.ContactID, ids.ListID, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}

	res, err := st.PruneList(ctx, store.ListPruneCandidatesParams{
		ListID:   ids.ListID,
		Criteria: store.PruneCriteria{MinMessagesSinceEngagement: 20},
		Limit:    10,
	}, false, 5)
	if err != nil {
		t.Fatal(err)
	}
	if res.Unsubscribed != 1 {
		t.Fatalf("Unsubscribed: want 1, got %d", res.Unsubscribed)
	}

	// Subscription row must be flipped.
	var subscribed int
	if err := d.QueryRowContext(ctx,
		`SELECT subscribed FROM subscriptions WHERE list_id = ? AND contact_id = ?`,
		ids.ListID, ids.ContactID).Scan(&subscribed); err != nil {
		t.Fatal(err)
	}
	if subscribed != 0 {
		t.Fatalf("subscription not unsubscribed: subscribed=%d", subscribed)
	}

	// Audit row must exist with the expected reason — proves atomicity.
	var reason sql.NullString
	if err := d.QueryRowContext(ctx,
		`SELECT reason FROM unsubscribe_events WHERE list_id = ? AND contact_id = ?`,
		ids.ListID, ids.ContactID).Scan(&reason); err != nil {
		t.Fatal(err)
	}
	if !reason.Valid || reason.String != "auto-prune-by-count" {
		t.Fatalf("audit reason: want auto-prune-by-count, got %+v", reason)
	}

	// Idempotency: re-running finds zero candidates.
	again, err := st.PruneList(ctx, store.ListPruneCandidatesParams{
		ListID:   ids.ListID,
		Criteria: store.PruneCriteria{MinMessagesSinceEngagement: 20},
		Limit:    10,
	}, false, 5)
	if err != nil {
		t.Fatal(err)
	}
	if again.Candidates != 0 || again.Unsubscribed != 0 {
		t.Fatalf("re-run: want 0/0, got %d/%d", again.Candidates, again.Unsubscribed)
	}
}

// worker_state KV helpers — round-trip a key/value, including the
// int64-typed convenience wrappers used by the auto-prune throttle.
func TestWorkerStateKV_RoundTrip(t *testing.T) {
	st, _ := newTestStore(t)
	ctx := context.Background()

	if _, ok, err := st.GetState(ctx, "missing"); err != nil || ok {
		t.Fatalf("missing key: want (false, nil), got ok=%v err=%v", ok, err)
	}
	if err := st.SetStateInt64(ctx, "auto_prune_last_run_ms", 12345); err != nil {
		t.Fatal(err)
	}
	v, ok, err := st.GetStateInt64(ctx, "auto_prune_last_run_ms")
	if err != nil || !ok || v != 12345 {
		t.Fatalf("round-trip: want (12345,true,nil), got (%d,%v,%v)", v, ok, err)
	}
	if err := st.SetStateInt64(ctx, "auto_prune_last_run_ms", 67890); err != nil {
		t.Fatal(err)
	}
	v, ok, err = st.GetStateInt64(ctx, "auto_prune_last_run_ms")
	if err != nil || !ok || v != 67890 {
		t.Fatalf("overwrite: want (67890,true,nil), got (%d,%v,%v)", v, ok, err)
	}
}
