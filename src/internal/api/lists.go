package api

import (
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/ranaroussi/minigun/internal/store"
)

var slugRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)

type createListReq struct {
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Company     string `json:"company"`
	Domain      string `json:"domain"`
	Description string `json:"description"`
	Weight      int    `json:"weight"`
}

func (s *Server) handleCreateList(w http.ResponseWriter, r *http.Request) {
	var req createListReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Slug = strings.ToLower(strings.TrimSpace(req.Slug))
	req.Company = strings.TrimSpace(req.Company)
	req.Domain = strings.ToLower(strings.TrimSpace(req.Domain))
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if !slugRE.MatchString(req.Slug) {
		writeError(w, http.StatusBadRequest, "slug must be lowercase alphanumerics or hyphens, 1-64 chars")
		return
	}
	if req.Company == "" {
		writeError(w, http.StatusBadRequest, "company is required (id or slug)")
		return
	}
	company, err := s.store.ResolveCompany(r.Context(), req.Company)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "company not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	sendingDomain := req.Domain
	if sendingDomain == "" {
		sendingDomain = company.SendingDomain
	}
	if sendingDomain == "" {
		writeError(w, http.StatusBadRequest, "domain is required and parent company has no sending_domain configured")
		return
	}
	weight := req.Weight
	if weight == 0 {
		weight = 10
	}
	l, err := s.store.CreateList(r.Context(), store.NewListParams{
		Slug:          req.Slug,
		Name:          req.Name,
		CompanyID:     company.ID,
		SendingDomain: sendingDomain,
		Description:   req.Description,
		Weight:        weight,
	})
	if err != nil {
		if errors.Is(err, store.ErrAlreadyExists) {
			writeError(w, http.StatusConflict, "list with that slug already exists")
			return
		}
		s.log.Error("create list", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, l)
}

type addContactReq struct {
	Email  string         `json:"email"`
	Params map[string]any `json:"params"`
}

func (s *Server) handleAddContact(w http.ResponseWriter, r *http.Request) {
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

	var req addContactReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.Email) == "" {
		writeError(w, http.StatusBadRequest, "email is required")
		return
	}

	contact, err := s.store.UpsertContact(r.Context(), req.Email, req.Params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	sub, err := s.store.UpsertSubscription(r.Context(), listID, contact.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"contact": contact,
		"subscription": map[string]any{
			"id":         sub.ID,
			"list_id":    sub.ListID,
			"contact_id": sub.ContactID,
			"subscribed": sub.Subscribed,
		},
	})
}

type listUnsubReq struct {
	Email string `json:"email"`
}

func (s *Server) handleListUnsubscribe(w http.ResponseWriter, r *http.Request) {
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
	var req listUnsubReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.Email) == "" {
		writeError(w, http.StatusBadRequest, "email is required")
		return
	}
	sub, err := s.store.UnsubscribeByListAndEmail(r.Context(), listID, req.Email)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "no subscription found for that email")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if _, err := s.store.RecordUnsubscribeEvent(r.Context(), nil, sub, req.Email); err != nil {
		s.log.Error("record unsub event", "err", err)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":              true,
		"subscription_id": sub.ID,
	})
}
