package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/ranaroussi/minigun/internal/store"
)

func (s *Server) handleListLists(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListLists(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if items == nil {
		items = []store.ListSummary{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleGetList(w http.ResponseWriter, r *http.Request) {
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
	d, err := s.store.GetListDetails(r.Context(), listID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "list not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) handleListContacts(w http.ResponseWriter, r *http.Request) {
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

	q := r.URL.Query()
	cursor, err := store.DecodeCursor(q.Get("cursor"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	limit := 0
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "limit must be an integer")
			return
		}
		limit = n
	}
	limit = store.ClampLimit(limit)

	items, err := s.store.ListContactsInList(r.Context(), listID, cursor.AfterIntID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var nextCursor string
	if len(items) == limit && len(items) > 0 {
		nextCursor = store.Cursor{AfterIntID: items[len(items)-1].SubscriptionID}.Encode()
	}
	if items == nil {
		items = []store.ListContact{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":       items,
		"next_cursor": nextCursor,
	})
}
