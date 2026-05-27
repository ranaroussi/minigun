package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/ranaroussi/minigun/internal/store"
)

// eventsCursor is the opaque keyset cursor we hand back to clients. The
// shape is intentionally minimal — just the (ts, id) tuple — so future
// schema changes can extend it without breaking existing pages.
type eventsCursor struct {
	Ts int64  `json:"t"`
	ID string `json:"i"`
}

func encodeEventsCursor(c eventsCursor) string {
	raw, _ := json.Marshal(c)
	return strings.TrimRight(base64.URLEncoding.EncodeToString(raw), "=")
}

func decodeEventsCursor(s string) (eventsCursor, error) {
	if s == "" {
		return eventsCursor{}, nil
	}
	pad := s
	if rem := len(pad) % 4; rem != 0 {
		pad += strings.Repeat("=", 4-rem)
	}
	raw, err := base64.URLEncoding.DecodeString(pad)
	if err != nil {
		return eventsCursor{}, errors.New("invalid cursor")
	}
	var c eventsCursor
	if err := json.Unmarshal(raw, &c); err != nil {
		return eventsCursor{}, errors.New("invalid cursor")
	}
	return c, nil
}

// handleListSendEvents — GET /sends/{id}/events.
//
// Query params:
//   event    — filter by event type (delivered/opened/clicked/...)
//   since    — lower bound on event_timestamp_ms (int ms epoch)
//   limit    — page size (default 100, max 500)
//   cursor   — opaque keyset cursor from a previous page's next_cursor
//
// Response: { items: [...], next_cursor: "..." }. next_cursor is empty
// when the page is the last one.
func (s *Server) handleListSendEvents(w http.ResponseWriter, r *http.Request) {
	sendID := strings.TrimSpace(chi.URLParam(r, "id"))
	if sendID == "" {
		writeError(w, http.StatusBadRequest, "send id required")
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	since, _ := strconv.ParseInt(q.Get("since"), 10, 64)

	cur, err := decodeEventsCursor(q.Get("cursor"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	events, err := s.store.ListSendEvents(r.Context(), store.ListSendEventsParams{
		SendID:    sendID,
		EventType: strings.TrimSpace(q.Get("event")),
		SinceMs:   since,
		AfterTsMs: cur.Ts,
		AfterID:   cur.ID,
		Limit:     limit,
	})
	if err != nil {
		s.log.Error("list send events", "send_id", sendID, "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	resp := map[string]any{
		"items": events,
	}
	// We emit a next_cursor when this page filled to the limit — even
	// if the next page would be empty, the next request will tell the
	// client so by returning an empty items array + no cursor.
	if len(events) == limit {
		last := events[len(events)-1]
		resp["next_cursor"] = encodeEventsCursor(eventsCursor{Ts: last.EventTimestampMs, ID: last.ID})
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleGetContactEngagement — GET /contacts/{idOrEmail}/engagement.
//
// Returns the per-list engagement summary for one contact. Optional
// `?list_id=` narrows to one list, otherwise returns one row per list
// the contact has been delivered/opened/clicked on.
//
// The endpoint is read-mostly and intentionally separate from
// /contacts/{id} (which doesn't exist yet — engagement is the only
// per-contact derived data we expose).
func (s *Server) handleGetContactEngagement(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimSpace(chi.URLParam(r, "idOrEmail"))
	if key == "" {
		writeError(w, http.StatusBadRequest, "id or email required")
		return
	}
	contactID, err := s.store.ResolveContactID(r.Context(), key)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "contact not found")
			return
		}
		s.log.Error("resolve contact", "key", key, "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	listID := strings.TrimSpace(r.URL.Query().Get("list_id"))
	// If they passed a slug instead of a list id, resolve it. We accept
	// both for symmetry with the rest of the API surface.
	if listID != "" {
		resolvedID, _, rerr := s.resolveList(r.Context(), listID)
		if rerr != nil {
			if errors.Is(rerr, store.ErrNotFound) {
				writeError(w, http.StatusNotFound, "list not found")
				return
			}
			s.log.Error("resolve list", "key", listID, "err", rerr)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		listID = resolvedID
	}
	rows, err := s.store.ListContactEngagement(r.Context(), contactID, listID)
	if err != nil {
		s.log.Error("list contact engagement", "contact_id", contactID, "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"contact_id": contactID,
		"items":      rows,
	})
}
