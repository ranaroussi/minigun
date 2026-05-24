package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/ranaroussi/minigun/internal/store"
)

func (s *Server) handleListSends(w http.ResponseWriter, r *http.Request) {
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

	items, err := s.store.ListSends(r.Context(), cursor.AfterCreated, cursor.AfterStringID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var nextCursor string
	if len(items) == limit && len(items) > 0 {
		last := items[len(items)-1]
		nextCursor = store.Cursor{
			AfterCreated:  last.CreatedAt.UTC().Format(time.RFC3339Nano),
			AfterStringID: last.ID,
		}.Encode()
	}
	if items == nil {
		items = []store.SendSummary{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":       items,
		"next_cursor": nextCursor,
	})
}
