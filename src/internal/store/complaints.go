package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ranaroussi/minigun/internal/ids"
)

type RecordComplaintInput struct {
	Email            string
	ContactID        string
	MailgunEventID   string
	MailgunTimestamp string
	Payload          any
}

// RecordComplaintEvent saves a spam-complaint signal received from
// Mailgun's webhook. Stored separately from unsubscribe_events because
// (a) the corresponding contact row will typically be deleted in the
// same webhook handler, so the audit trail must survive without an
// FK to contacts, and (b) complaints are a stronger compliance signal
// than ordinary opt-outs and warrant their own retention policy.
//
// MailgunEventID is UNIQUE in the schema; INSERT OR IGNORE makes
// webhook retries idempotent without us tracking dedupe state.
func (s *Store) RecordComplaintEvent(ctx context.Context, in RecordComplaintInput) error {
	var payloadStr any
	switch p := in.Payload.(type) {
	case nil:
		payloadStr = nil
	case string:
		payloadStr = p
	default:
		b, err := json.Marshal(p)
		if err != nil {
			return fmt.Errorf("marshal complaint payload: %w", err)
		}
		payloadStr = string(b)
	}
	id := ids.NewComplaint()
	if _, err := s.DB.ExecContext(ctx,
		`INSERT OR IGNORE INTO complaint_events
		   (id, email, contact_id, mailgun_event_id, mailgun_timestamp, payload, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, strings.ToLower(strings.TrimSpace(in.Email)),
		nullStringPtr(in.ContactID), nullStringPtr(in.MailgunEventID),
		nullStringPtr(in.MailgunTimestamp), payloadStr, nowISO(),
	); err != nil {
		return fmt.Errorf("insert complaint_event: %w", err)
	}
	return nil
}

func nullStringPtr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
