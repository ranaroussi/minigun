package api

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/ranaroussi/minigun/internal/store"
)

// handleListSendRecipients — GET /send/{id}/recipients.
//
// Returns the per-recipient message engagement rollup for a send (one
// row per contact: sent/delivered/open/click/failure timestamps + counts),
// keyset-paginated by contact_id.
//
// Query params:
//   limit   — page size (default 100, max 500)
//   cursor  — opaque cursor (last contact_id) from a previous page
//
// Response: { items: [...], next_cursor: "..." }.
func (s *Server) handleListSendRecipients(w http.ResponseWriter, r *http.Request) {
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
	// Cursor is the last contact_id of the previous page, base64-encoded
	// to keep the wire shape opaque/extensible.
	after := ""
	if cur := q.Get("cursor"); cur != "" {
		pad := cur
		if rem := len(pad) % 4; rem != 0 {
			pad += strings.Repeat("=", 4-rem)
		}
		raw, err := base64.URLEncoding.DecodeString(pad)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid cursor")
			return
		}
		after = string(raw)
	}
	rows, err := s.store.ListSendRecipients(r.Context(), store.ListSendRecipientsParams{
		SendID:         sendID,
		AfterContactID: after,
		Limit:          limit,
	})
	if err != nil {
		s.log.Error("list send recipients", "send_id", sendID, "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	resp := map[string]any{"items": rows}
	if len(rows) == limit {
		last := rows[len(rows)-1]
		resp["next_cursor"] = strings.TrimRight(base64.URLEncoding.EncodeToString([]byte(last.ContactID)), "=")
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleListSendClicks — GET /send/{id}/clicks.
//
// Returns the per-URL click rollup for a send (one row per
// (contact, canonical url): first/last click + click count),
// keyset-paginated over the composite (contact_id, url).
//
// Query params:
//   limit   — page size (default 100, max 500)
//   cursor  — opaque cursor from a previous page
//
// Response: { items: [...], next_cursor: "..." }.
func (s *Server) handleListSendClicks(w http.ResponseWriter, r *http.Request) {
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
	// Cursor packs the last (contact_id, url) of the previous page as
	// "contact_id\nurl", base64-encoded to stay opaque. contact_id (c_*)
	// never contains a newline, so the first \n is an unambiguous split.
	afterContactID, afterURL := "", ""
	if cur := q.Get("cursor"); cur != "" {
		pad := cur
		if rem := len(pad) % 4; rem != 0 {
			pad += strings.Repeat("=", 4-rem)
		}
		raw, err := base64.URLEncoding.DecodeString(pad)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid cursor")
			return
		}
		parts := strings.SplitN(string(raw), "\n", 2)
		afterContactID = parts[0]
		if len(parts) == 2 {
			afterURL = parts[1]
		}
	}
	rows, err := s.store.ListSendClicks(r.Context(), store.ListSendClicksParams{
		SendID:         sendID,
		AfterContactID: afterContactID,
		AfterURL:       afterURL,
		Limit:          limit,
	})
	if err != nil {
		s.log.Error("list send clicks", "send_id", sendID, "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	resp := map[string]any{"items": rows}
	if len(rows) == limit {
		last := rows[len(rows)-1]
		packed := last.ContactID + "\n" + last.URL
		resp["next_cursor"] = strings.TrimRight(base64.URLEncoding.EncodeToString([]byte(packed)), "=")
	}
	writeJSON(w, http.StatusOK, resp)
}

// handlePruneList — POST /lists/{list}/prune
//
// Body shape:
//   {
//     "min_messages_since_engagement": int,  // 0 disables this criterion
//     "dormant_for_days":              int,  // 0 disables this criterion
//     "no_delivery_for_days":          int,  // 0 disables this criterion
//     "dry_run":                       bool, // default TRUE — fail-safe
//     "limit":                         int,  // default 1000, max 10000
//     "sample_size":                   int   // sample rows to return (default 25)
//   }
//
// Returns: { list_id, dry_run, candidates, unsubscribed, sample, reason_counts }.
//
// The endpoint is DESTRUCTIVE when dry_run=false. The default-true dry_run
// is the safety contract: operators who forget to pass anything get a preview,
// not a purge.
func (s *Server) handlePruneList(w http.ResponseWriter, r *http.Request) {
	listKey := chi.URLParam(r, "list")
	listID, _, err := s.resolveList(r.Context(), listKey)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "list not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var req struct {
		MinMessagesSinceEngagement int64 `json:"min_messages_since_engagement"`
		DormantForDays             int64 `json:"dormant_for_days"`
		NoDeliveryForDays          int64 `json:"no_delivery_for_days"`
		// We use *bool so we can distinguish "omitted (default true)" from
		// "explicitly set to false." Operators must opt-IN to destructive
		// mode by setting dry_run=false.
		DryRun     *bool `json:"dry_run"`
		Limit      int   `json:"limit"`
		SampleSize int   `json:"sample_size"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	dryRun := true
	if req.DryRun != nil {
		dryRun = *req.DryRun
	}

	const dayMs int64 = 24 * 60 * 60 * 1000
	criteria := store.PruneCriteria{
		MinMessagesSinceEngagement: req.MinMessagesSinceEngagement,
		DormantForMs:               req.DormantForDays * dayMs,
		NoDeliveryForMs:            req.NoDeliveryForDays * dayMs,
	}
	if !criteria.HasAny() {
		writeError(w, http.StatusBadRequest, "at least one of min_messages_since_engagement, dormant_for_days, no_delivery_for_days must be > 0")
		return
	}

	result, err := s.store.PruneList(r.Context(), store.ListPruneCandidatesParams{
		ListID:   listID,
		Criteria: criteria,
		Limit:    req.Limit,
	}, dryRun, req.SampleSize)
	if err != nil {
		s.log.Error("prune list", "list_id", listID, "err", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
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
