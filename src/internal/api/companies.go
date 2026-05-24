package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/ranaroussi/minigun/internal/store"
)

type createCompanyReq struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

func (s *Server) handleCreateCompany(w http.ResponseWriter, r *http.Request) {
	var req createCompanyReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Slug = strings.ToLower(strings.TrimSpace(req.Slug))
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if !slugRE.MatchString(req.Slug) {
		writeError(w, http.StatusBadRequest, "slug must be lowercase alphanumerics or hyphens, 1-64 chars")
		return
	}
	c, err := s.store.CreateCompany(r.Context(), req.Slug, req.Name)
	if err != nil {
		if errors.Is(err, store.ErrAlreadyExists) {
			writeError(w, http.StatusConflict, "company with that slug already exists")
			return
		}
		s.log.Error("create company", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (s *Server) handleListCompanies(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListCompanies(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if items == nil {
		items = []store.CompanySummary{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleGetCompany(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "company")
	c, err := s.store.ResolveCompany(r.Context(), key)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "company not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) handleListCompanyLists(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "company")
	c, err := s.store.ResolveCompany(r.Context(), key)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "company not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	items, err := s.store.ListsForCompany(r.Context(), c.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"company": c,
		"items":   items,
	})
}
