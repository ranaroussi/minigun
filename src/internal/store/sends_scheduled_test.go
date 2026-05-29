package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/ranaroussi/minigun/internal/models"
	"github.com/ranaroussi/minigun/internal/store"
)

func strptr(s string) *string { return &s }

func newScheduledSingle(t *testing.T, st *store.Store, sendAt *time.Time) *models.Send {
	t.Helper()
	snd, err := st.CreateSend(context.Background(), store.NewSendParams{
		Type:           models.SendTypeSingle,
		RecipientEmail: strptr("a@b.com"),
		Subject:        "Hi",
		FromHeader:     "Ran <r@x.com>",
		SendingDomain:  "mg.example.com",
		SendAt:         sendAt,
	})
	if err != nil {
		t.Fatalf("CreateSend: %v", err)
	}
	return snd
}

// A future send_at parks the send in 'scheduled' with send_at persisted; a
// nil or past send_at sends now ('queued', send_at NULL).
func TestCreateSend_SchedulingDecision(t *testing.T) {
	st, _ := newTestStore(t)

	future := time.Now().Add(time.Hour)
	sched := newScheduledSingle(t, st, &future)
	if sched.Status != models.SendStatusScheduled {
		t.Fatalf("future send_at: want status scheduled, got %s", sched.Status)
	}
	if sched.SendAt == nil {
		t.Fatal("future send_at: want SendAt persisted, got nil")
	}

	past := time.Now().Add(-time.Hour)
	pastSnd := newScheduledSingle(t, st, &past)
	if pastSnd.Status != models.SendStatusQueued {
		t.Fatalf("past send_at: want status queued (send now), got %s", pastSnd.Status)
	}
	if pastSnd.SendAt != nil {
		t.Fatalf("past send_at: want SendAt nil, got %v", pastSnd.SendAt)
	}

	none := newScheduledSingle(t, st, nil)
	if none.Status != models.SendStatusQueued {
		t.Fatalf("no send_at: want status queued, got %s", none.Status)
	}
}

// A scheduled bulk send is parked with no frozen audience (nil
// max_subscription_id); SetSendAudience resolves it at dispatch.
func TestScheduledBulkAudienceDeferredToDispatch(t *testing.T) {
	st, d := newTestStore(t)
	ids := seed(t, d)
	ctx := context.Background()

	future := time.Now().Add(time.Hour)
	snd, err := st.CreateSend(ctx, store.NewSendParams{
		Type:              models.SendTypeBulk,
		ListID:            &ids.ListID,
		Subject:           "Hi",
		FromHeader:        "Ran <r@x.com>",
		SendingDomain:     ids.Domain,
		MaxSubscriptionID: nil, // deferred — resolved at dispatch
		SendAt:            &future,
	})
	if err != nil {
		t.Fatalf("CreateSend: %v", err)
	}
	if snd.Status != models.SendStatusScheduled {
		t.Fatalf("want status scheduled, got %s", snd.Status)
	}
	if snd.MaxSubscriptionID != nil {
		t.Fatalf("scheduled bulk should not freeze audience at creation, got max=%v", *snd.MaxSubscriptionID)
	}

	// Dispatch resolves and persists the audience snapshot.
	if err := st.SetSendAudience(ctx, snd.ID, 42, 7); err != nil {
		t.Fatalf("SetSendAudience: %v", err)
	}
	got, err := st.GetSend(ctx, snd.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.MaxSubscriptionID == nil || *got.MaxSubscriptionID != 42 {
		t.Fatalf("want max_subscription_id=42 after dispatch, got %v", got.MaxSubscriptionID)
	}
	if got.TotalRecipients != 7 {
		t.Fatalf("want total_recipients=7 after dispatch, got %d", got.TotalRecipients)
	}
}

// ListDueScheduledSends returns only scheduled rows whose send_at has passed.
func TestListDueScheduledSends(t *testing.T) {
	st, d := newTestStore(t)
	ctx := context.Background()

	future := time.Now().Add(time.Hour)
	notYet := newScheduledSingle(t, st, &future)
	due := newScheduledSingle(t, st, &future)

	// Simulate time passing for one of them by backdating its send_at.
	if _, err := d.ExecContext(ctx,
		`UPDATE sends SET send_at = ? WHERE id = ?`,
		time.Now().Add(-time.Minute).UTC().Format(time.RFC3339), due.ID); err != nil {
		t.Fatal(err)
	}

	ids, err := st.ListDueScheduledSends(ctx, 10)
	if err != nil {
		t.Fatalf("ListDueScheduledSends: %v", err)
	}
	if len(ids) != 1 || ids[0] != due.ID {
		t.Fatalf("want exactly the due send %s, got %v", due.ID, ids)
	}
	for _, id := range ids {
		if id == notYet.ID {
			t.Fatalf("not-yet-due send %s should not be returned", notYet.ID)
		}
	}
}

// CancelScheduledSend transitions scheduled/queued -> cancelled, and the
// guarded WHERE refuses to cancel anything already running or terminal.
func TestCancelScheduledSend(t *testing.T) {
	st, d := newTestStore(t)
	ctx := context.Background()

	future := time.Now().Add(time.Hour)
	snd := newScheduledSingle(t, st, &future)

	ok, err := st.CancelScheduledSend(ctx, snd.ID)
	if err != nil {
		t.Fatalf("CancelScheduledSend: %v", err)
	}
	if !ok {
		t.Fatal("want cancelled=true for a scheduled send")
	}
	got, err := st.GetSend(ctx, snd.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != models.SendStatusCancelled {
		t.Fatalf("want status cancelled, got %s", got.Status)
	}

	// Cancelling again is a no-op (already terminal).
	if ok, _ := st.CancelScheduledSend(ctx, snd.ID); ok {
		t.Fatal("second cancel should report false")
	}

	// A running send cannot be cancelled via the guarded path.
	running := newScheduledSingle(t, st, &future)
	if _, err := d.ExecContext(ctx,
		`UPDATE sends SET status = 'running' WHERE id = ?`, running.ID); err != nil {
		t.Fatal(err)
	}
	if ok, _ := st.CancelScheduledSend(ctx, running.ID); ok {
		t.Fatal("cancel of a running send should report false")
	}
}
