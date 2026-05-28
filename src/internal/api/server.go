package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/ranaroussi/minigun/internal/config"
	"github.com/ranaroussi/minigun/internal/mailgun"
	"github.com/ranaroussi/minigun/internal/store"
	"github.com/ranaroussi/minigun/internal/turnstile"
	"github.com/ranaroussi/minigun/internal/worker"
)

type Server struct {
	cfg       *config.Config
	store     *store.Store
	mailgun   *mailgun.Client
	worker    *worker.Manager
	turnstile *turnstile.Verifier
	log       *slog.Logger
	router    chi.Router
}

func New(cfg *config.Config, st *store.Store, mg *mailgun.Client, wm *worker.Manager, ts *turnstile.Verifier, log *slog.Logger) *Server {
	s := &Server{
		cfg:       cfg,
		store:     st,
		mailgun:   mg,
		worker:    wm,
		turnstile: ts,
		log:       log,
	}
	s.router = s.routes()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.router.ServeHTTP(w, r)
}

func (s *Server) routes() chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))
	r.Use(s.bearerAuth)

	r.Get("/healthz", s.handleHealth)

	r.Get("/companies", s.handleListCompanies)
	r.Post("/companies", s.handleCreateCompany)
	r.Get("/companies/{company}", s.handleGetCompany)
	r.Get("/companies/{company}/lists", s.handleListCompanyLists)

	r.Get("/lists", s.handleListLists)
	r.Post("/lists", s.handleCreateList)
	r.Get("/lists/{list}", s.handleGetList)
	r.Get("/lists/{list}/contacts", s.handleListContacts)
	r.Post("/lists/{list}/contacts", s.handleAddContact)
	r.Post("/lists/{list}/unsubscribe", s.handleListUnsubscribe)
	r.Post("/lists/{list}/prune", s.handlePruneList)

	r.Delete("/contacts/{idOrEmail}", s.handleDeleteContact)

	r.Post("/webhooks/mailgun", s.handleMailgunWebhook)

	r.Get("/sends", s.handleListSends)
	r.Post("/send/bulk", s.handleBulkSend)
	r.Post("/send/single", s.handleSingleSend)
	r.Post("/send/{id}/resume", s.handleResumeSend)
	r.Post("/send/{id}/next", s.handleResumeSend)
	r.Get("/send/{id}", s.handleGetSend)
	r.Get("/send/{id}/stats", s.handleSendStats)
	r.Get("/send/{id}/events", s.handleListSendEvents)

	r.Get("/contacts/{idOrEmail}/engagement", s.handleGetContactEngagement)

	r.Get("/u/{token}", s.handleUnsubscribeGet)
	r.Post("/u/{token}", s.handleUnsubscribePost)
	r.Get("/manage/{token}", s.handleManageGet)
	r.Post("/manage/{token}", s.handleManagePost)

	return r
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err string) {
	writeJSON(w, status, map[string]string{"error": err})
}

func decodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func (s *Server) resolveList(ctx context.Context, idOrSlug string) (string, string, error) {
	l, err := s.store.ResolveList(ctx, idOrSlug)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return "", "", err
		}
		return "", "", err
	}
	return l.ID, l.Name, nil
}
