package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ranaroussi/minigun/internal/mailgun"
	"github.com/ranaroussi/minigun/internal/store"
)

// mailgunWebhookPayload covers only the fields we dispatch on. Mailgun
// includes far more — we round-trip the whole `event-data` object into
// the audit log for complaints so nothing's lost.
type mailgunWebhookPayload struct {
	Signature struct {
		Timestamp string `json:"timestamp"`
		Token     string `json:"token"`
		Signature string `json:"signature"`
	} `json:"signature"`
	EventData map[string]any `json:"event-data"`
}

func (s *Server) handleMailgunWebhook(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	defer r.Body.Close()

	var body mailgunWebhookPayload
	if err := json.Unmarshal(raw, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	if err := mailgun.VerifyWebhookSignature(s.cfg.MailgunWebhookSigningKey, mailgun.WebhookSignature{
		Timestamp: body.Signature.Timestamp,
		Token:     body.Signature.Token,
		Signature: body.Signature.Signature,
	}, time.Now()); err != nil {
		// Log the reason internally but don't leak which check failed.
		s.log.Warn("mailgun webhook rejected", "reason", err.Error())
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	ev := body.EventData
	event, _ := ev["event"].(string)
	severity, _ := ev["severity"].(string)
	recipient := strings.ToLower(strings.TrimSpace(stringOf(ev["recipient"])))

	var mgEventID, mgTimestamp string
	mgEventID = stringOf(ev["id"])
	switch v := ev["timestamp"].(type) {
	case float64:
		mgTimestamp = strconv.FormatFloat(v, 'f', -1, 64)
	case string:
		mgTimestamp = v
	}

	// Dispatch matrix mirrors the worker:
	//   failed + permanent → DeleteContact
	//   complained         → record + DeleteContact
	//   everything else    → 200 OK no-op (Mailgun stops retrying)
	switch {
	case event == "failed" && severity == "permanent":
		if recipient == "" {
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "action": "noop-no-recipient"})
			return
		}
		result, err := s.store.DeleteContact(r.Context(), recipient)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeJSON(w, http.StatusOK, map[string]any{"ok": true, "action": "already-gone"})
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":                    true,
			"action":                "deleted",
			"contact_id":            result.Contact.ID,
			"subscriptions_removed": result.SubscriptionsRemoved,
		})

	case event == "complained":
		if recipient == "" {
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "action": "noop-no-recipient"})
			return
		}
		var contactID string
		if result, err := s.store.DeleteContact(r.Context(), recipient); err == nil {
			contactID = result.Contact.ID
		} else if !errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		// Always record the complaint (even if the contact was already
		// gone from a prior bounce) so the audit trail captures the
		// signal regardless of cleanup ordering.
		if err := s.store.RecordComplaintEvent(r.Context(), store.RecordComplaintInput{
			Email:            recipient,
			ContactID:        contactID,
			MailgunEventID:   mgEventID,
			MailgunTimestamp: mgTimestamp,
			Payload:          ev,
		}); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":         true,
			"action":     "complained",
			"contact_id": contactID,
		})

	default:
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "action": "noop", "event": event})
	}
}

func stringOf(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
