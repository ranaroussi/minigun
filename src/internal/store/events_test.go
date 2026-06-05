package store_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/ranaroussi/minigun/internal/db"
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

func TestLookupContactIDByEmail(t *testing.T) {
	st, d := newTestStore(t)
	ids := seed(t, d)
	ctx := context.Background()

	got, err := st.LookupContactIDByEmail(ctx, ids.ContactEM)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Valid || got.String != ids.ContactID {
		t.Fatalf("resolved: want %s, got %+v", ids.ContactID, got)
	}

	missing, err := st.LookupContactIDByEmail(ctx, "nobody@nowhere.test")
	if err != nil {
		t.Fatal(err)
	}
	if missing.Valid {
		t.Fatalf("unknown address should resolve to invalid NullString, got %+v", missing)
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

// A burst-due send must not be starved behind a large backlog of
// not-yet-due daily sends. The queue is ordered by
// events_last_pulled_at_ms ASC; a fresh send waiting on its +1h beat was
// pulled more recently than the daily backlog, so it sorts BEHIND them.
// Pre-fix the SQL fetched the oldest LIMIT rows then filtered due-ness in
// memory, so the not-due daily sends filled the window and the burst send
// never surfaced. The due predicate now lives in SQL, so it surfaces even
// with a small LIMIT.
func TestListDueEventPulls_BurstSendNotStarvedByBacklog(t *testing.T) {
	st, d := newTestStore(t)
	ctx := context.Background()
	if _, err := d.ExecContext(ctx, `
		INSERT INTO companies (id, slug, name, sending_domain, created_at, updated_at)
		VALUES ('co_starve', 'starve', 'Starve', 'mg.x.com', datetime('now'), datetime('now'))`); err != nil {
		t.Fatal(err)
	}
	nowMs := time.Now().UnixMilli()
	// 50 daily-phase sends pulled 2h ago — NOT due (need 24h). They sort
	// ahead (older last_pulled) of the burst send below.
	for i := 0; i < 50; i++ {
		id := fmt.Sprintf("s_daily_%02d", i)
		if _, err := d.ExecContext(ctx, `
			INSERT INTO sends (
			  id, type, subject, from_header, sending_domain, status,
			  batch_size, throttle_ms, test_mode, last_subscription_id,
			  total_recipients, unsubscribe_mode, created_at, updated_at, completed_at,
			  events_pulls_count, events_last_pulled_at_ms
			) VALUES (?, 'bulk', 'D', 'r@x.com', 'mg.x.com', 'completed',
			          500, 1000, 0, 0, 100, 'local',
			          datetime('now', '-10 days'), datetime('now', '-10 days'), datetime('now', '-10 days'),
			          5, ?)`,
			id, nowMs-2*60*60*1000,
		); err != nil {
			t.Fatal(err)
		}
	}
	// One burst send: created 90m ago, +0 pull done 1h ago, so its +1h
	// beat is due. It sorts behind the backlog (pulled more recently).
	if _, err := d.ExecContext(ctx, `
		INSERT INTO sends (
		  id, type, subject, from_header, sending_domain, status,
		  batch_size, throttle_ms, test_mode, last_subscription_id,
		  total_recipients, unsubscribe_mode, created_at, updated_at, completed_at,
		  events_pulls_count, events_last_pulled_at_ms
		) VALUES ('s_burst', 'bulk', 'B', 'r@x.com', 'mg.x.com', 'completed',
		          500, 1000, 0, 0, 100, 'local',
		          datetime('now', '-90 minutes'), datetime('now', '-90 minutes'), datetime('now', '-90 minutes'),
		          1, ?)`,
		nowMs-60*60*1000,
	); err != nil {
		t.Fatal(err)
	}
	rows, err := st.ListDueEventPulls(ctx, nowMs, 30*24*60*60*1000, 5)
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, r := range rows {
		have[r.SendID] = true
	}
	if !have["s_burst"] {
		t.Fatalf("burst-due send starved behind not-due daily backlog; got %d rows: %v", len(rows), have)
	}
	for id := range have {
		if id != "s_burst" {
			t.Fatalf("not-due daily send %s should not be returned", id)
		}
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

// ---------------------------------------------------------------------------
// Phase 6: per-(send, contact) message engagement
// ---------------------------------------------------------------------------

// A message's full lifecycle (accepted → delivered → open×2 → click →
// failed) folds into one cme row with idempotent timestamps + exact
// counts. Timestamps are epoch SECONDS.
func TestApplyEventToMessageEngagement_Lifecycle(t *testing.T) {
	st, d := newTestStore(t)
	ids := seed(t, d)
	ctx := context.Background()
	listNS := sql.NullString{String: ids.ListID, Valid: true}
	base := ids.CreatedAt.Unix()

	apply := func(event string, tsSec int64, sev, reason string) {
		t.Helper()
		if err := st.ApplyEventToMessageEngagement(ctx, ids.SendID, ids.ContactID, listNS, event, tsSec, optNS(sev), optNS(reason)); err != nil {
			t.Fatalf("apply %s: %v", event, err)
		}
	}
	apply("accepted", base+1, "", "")
	apply("delivered", base+2, "", "")
	apply("opened", base+10, "", "")
	apply("opened", base+20, "", "")
	apply("clicked", base+30, "", "")
	apply("failed", base+40, "temporary", "greylisted")

	rows, err := st.ListSendRecipients(ctx, store.ListSendRecipientsParams{SendID: ids.SendID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 recipient row, got %d", len(rows))
	}
	r := rows[0]
	if r.SentAt.Int64 != base+1 || r.DeliveredAt.Int64 != base+2 {
		t.Fatalf("sent/delivered: got %d/%d", r.SentAt.Int64, r.DeliveredAt.Int64)
	}
	if r.FirstOpenAt.Int64 != base+10 || r.LastOpenAt.Int64 != base+20 || r.TotalOpens != 2 {
		t.Fatalf("opens: first=%d last=%d total=%d", r.FirstOpenAt.Int64, r.LastOpenAt.Int64, r.TotalOpens)
	}
	if r.FirstClickAt.Int64 != base+30 || r.TotalClicks != 1 {
		t.Fatalf("clicks: first=%d total=%d", r.FirstClickAt.Int64, r.TotalClicks)
	}
	if r.Failed != 1 || r.FailedAt.Int64 != base+40 || r.FailureSeverity.String != "temporary" || r.FailureReason.String != "greylisted" {
		t.Fatalf("failure: %+v", r)
	}
}

// Out-of-order opens converge: first_open_at stays earliest, last_open_at
// stays latest regardless of arrival order.
func TestApplyEventToMessageEngagement_OutOfOrderConverges(t *testing.T) {
	st, d := newTestStore(t)
	ids := seed(t, d)
	ctx := context.Background()
	listNS := sql.NullString{String: ids.ListID, Valid: true}
	base := ids.CreatedAt.Unix()

	// Later open first, then earlier open.
	if err := st.ApplyEventToMessageEngagement(ctx, ids.SendID, ids.ContactID, listNS, "opened", base+100, sql.NullString{}, sql.NullString{}); err != nil {
		t.Fatal(err)
	}
	if err := st.ApplyEventToMessageEngagement(ctx, ids.SendID, ids.ContactID, listNS, "opened", base+10, sql.NullString{}, sql.NullString{}); err != nil {
		t.Fatal(err)
	}
	rows, err := st.ListSendRecipients(ctx, store.ListSendRecipientsParams{SendID: ids.SendID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	r := rows[0]
	if r.FirstOpenAt.Int64 != base+10 {
		t.Fatalf("first_open_at: want %d, got %d", base+10, r.FirstOpenAt.Int64)
	}
	if r.LastOpenAt.Int64 != base+100 {
		t.Fatalf("last_open_at: want %d, got %d", base+100, r.LastOpenAt.Int64)
	}
	if r.TotalOpens != 2 {
		t.Fatalf("total_opens: want 2, got %d", r.TotalOpens)
	}
}

// ListSendRecipients keyset-paginates by contact_id.
func TestListSendRecipients_Pagination(t *testing.T) {
	st, d := newTestStore(t)
	ids := seed(t, d)
	ctx := context.Background()
	listNS := sql.NullString{String: ids.ListID, Valid: true}
	base := ids.CreatedAt.Unix()

	// Add two more contacts so the send has three recipients.
	for _, cid := range []string{"c_aaa0000001", "c_bbb0000002"} {
		if _, err := d.ExecContext(ctx, `
			INSERT INTO contacts (id, email, params, created_at, updated_at)
			VALUES (?, ?, '{}', datetime('now'), datetime('now'))`,
			cid, cid+"@example.com"); err != nil {
			t.Fatal(err)
		}
		if err := st.ApplyEventToMessageEngagement(ctx, ids.SendID, cid, listNS, "delivered", base+1, sql.NullString{}, sql.NullString{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.ApplyEventToMessageEngagement(ctx, ids.SendID, ids.ContactID, listNS, "delivered", base+1, sql.NullString{}, sql.NullString{}); err != nil {
		t.Fatal(err)
	}

	page1, err := st.ListSendRecipients(ctx, store.ListSendRecipientsParams{SendID: ids.SendID, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(page1) != 2 {
		t.Fatalf("page1: want 2, got %d", len(page1))
	}
	page2, err := st.ListSendRecipients(ctx, store.ListSendRecipientsParams{
		SendID:         ids.SendID,
		AfterContactID: page1[1].ContactID,
		Limit:          10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page2) != 1 {
		t.Fatalf("page2: want 1, got %d", len(page2))
	}
	if page2[0].ContactID <= page1[1].ContactID {
		t.Fatalf("keyset broken: page2 %s not after page1 %s", page2[0].ContactID, page1[1].ContactID)
	}
}

func optNS(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

// ApplyClickToURL canonicalizes the link (lowercase scheme+host, strip
// query + fragment, preserve path case) and aggregates repeat clicks of
// the same canonical URL into one row with MIN/MAX timestamps.
func TestApplyClickToURL_CanonicalizeAndAggregate(t *testing.T) {
	st, d := newTestStore(t)
	ids := seed(t, d)
	ctx := context.Background()
	listNS := sql.NullString{String: ids.ListID, Valid: true}
	base := ids.CreatedAt.Unix()

	// Two clicks of the "same" link with different case/query/fragment +
	// out-of-order timestamps must collapse to one canonical row.
	if err := st.ApplyClickToURL(ctx, ids.SendID, ids.ContactID, listNS, "HTTPS://Example.COM/Path?utm=1#frag", base+50); err != nil {
		t.Fatal(err)
	}
	if err := st.ApplyClickToURL(ctx, ids.SendID, ids.ContactID, listNS, "https://example.com/Path?utm=2", base+10); err != nil {
		t.Fatal(err)
	}
	// Empty URL is a no-op.
	if err := st.ApplyClickToURL(ctx, ids.SendID, ids.ContactID, listNS, "   ", base+99); err != nil {
		t.Fatal(err)
	}

	rows, err := st.ListSendClicks(ctx, store.ListSendClicksParams{SendID: ids.SendID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 canonical click row, got %d", len(rows))
	}
	r := rows[0]
	if r.URL != "https://example.com/Path" {
		t.Fatalf("canonical url: want https://example.com/Path, got %q", r.URL)
	}
	if r.TotalClicks != 2 {
		t.Fatalf("total_clicks: want 2, got %d", r.TotalClicks)
	}
	if !r.FirstClickAt.Valid || r.FirstClickAt.Int64 != base+10 {
		t.Fatalf("first_click_at: want %d (MIN), got %v", base+10, r.FirstClickAt)
	}
	if !r.LastClickAt.Valid || r.LastClickAt.Int64 != base+50 {
		t.Fatalf("last_click_at: want %d (MAX), got %v", base+50, r.LastClickAt)
	}
}

// ListSendClicks keyset-paginates over the composite (contact_id, url).
func TestListSendClicks_Pagination(t *testing.T) {
	st, d := newTestStore(t)
	ids := seed(t, d)
	ctx := context.Background()
	listNS := sql.NullString{String: ids.ListID, Valid: true}
	base := ids.CreatedAt.Unix()

	urls := []string{"https://example.com/a", "https://example.com/b", "https://example.com/c"}
	for _, u := range urls {
		if err := st.ApplyClickToURL(ctx, ids.SendID, ids.ContactID, listNS, u, base+1); err != nil {
			t.Fatal(err)
		}
	}

	page1, err := st.ListSendClicks(ctx, store.ListSendClicksParams{SendID: ids.SendID, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(page1) != 2 {
		t.Fatalf("page1: want 2, got %d", len(page1))
	}
	last := page1[len(page1)-1]
	page2, err := st.ListSendClicks(ctx, store.ListSendClicksParams{
		SendID:         ids.SendID,
		AfterContactID: last.ContactID,
		AfterURL:       last.URL,
		Limit:          10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page2) != 1 {
		t.Fatalf("page2: want 1, got %d", len(page2))
	}
	if page2[0].URL <= last.URL {
		t.Fatalf("keyset broken: page2 url %q not after page1 %q", page2[0].URL, last.URL)
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
