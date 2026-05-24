package api

import (
	"net/http"
)

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	dbOK := "ok"
	if err := s.store.DB.PingContext(r.Context()); err != nil {
		dbOK = "error"
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"status": "error",
			"db":     dbOK,
			"error":  err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"status": "ok",
		"db":     dbOK,
	})
}
