package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ranaroussi/minigun/internal/mailgun"
	"github.com/ranaroussi/minigun/internal/models"
	"github.com/ranaroussi/minigun/internal/render"
	"github.com/ranaroussi/minigun/internal/store"
)

type bulkSendReq struct {
	List       string `json:"list"`
	Subject    string `json:"subject"`
	Preheader  string `json:"preheader"`
	From       string `json:"from"`
	ReplyTo    string `json:"reply_to"`
	MD         string `json:"md"`
	HTML       string `json:"html"`
	Text       string `json:"text"`
	Template   string `json:"template"`
	BatchSize  int    `json:"batch_size"`
	ThrottleMS int    `json:"throttle_ms"`

	UnsubMode    string `json:"unsub_mode"`
	UnsubRedir   string `json:"unsub_redir"`
	UnsubURL     string `json:"unsub_url"`
	NotifyEmail  string `json:"notify_email"`
}

func (s *Server) handleBulkSend(w http.ResponseWriter, r *http.Request) {
	var req bulkSendReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.List) == "" || strings.TrimSpace(req.Subject) == "" || strings.TrimSpace(req.From) == "" {
		writeError(w, http.StatusBadRequest, "list, subject, and from are required")
		return
	}
	if req.MD == "" && req.HTML == "" {
		writeError(w, http.StatusBadRequest, "either md or html is required")
		return
	}

	listID, _, err := s.resolveList(r.Context(), req.List)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "list not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var bodyHTML, bodyText string
	if req.MD != "" {
		bodyHTML, bodyText, _, err = render.BuildBody(req.MD, "", req.Subject, req.Preheader)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	} else {
		htmlWithFooter := render.EnsureUnsubFooterHTML(req.HTML)
		rewritten, _ := render.RewriteVariables(htmlWithFooter)
		bodyHTML = rewritten
		if req.Text != "" {
			rewrittenText, _ := render.RewriteVariables(render.EnsureUnsubFooterText(req.Text))
			bodyText = rewrittenText
		} else {
			derived := render.EnsureUnsubFooterText(render.HTMLToText(req.HTML))
			rewrittenText, _ := render.RewriteVariables(derived)
			bodyText = rewrittenText
		}
	}

	maxID, err := s.store.MaxSubscriptionID(r.Context(), listID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	total, err := s.store.CountSubscribed(r.Context(), listID, maxID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	mode := models.UnsubModeLocal
	if req.UnsubMode != "" {
		mode = models.UnsubscribeMode(req.UnsubMode)
	}

	params := store.NewSendParams{
		Type:                   models.SendTypeBulk,
		ListID:                 &listID,
		Subject:                req.Subject,
		FromHeader:             req.From,
		ReplyTo:                emptyToNil(req.ReplyTo),
		TemplateName:           emptyToNil(req.Template),
		BodyMD:                 emptyToNil(req.MD),
		BodyHTML:               &bodyHTML,
		BodyText:               &bodyText,
		BatchSize:              req.BatchSize,
		ThrottleMS:             req.ThrottleMS,
		MaxSubscriptionID:      &maxID,
		TotalRecipients:        total,
		UnsubscribeMode:        mode,
		UnsubscribeRedirectURL: emptyToNil(req.UnsubRedir),
		UnsubscribeExternalURL: emptyToNil(req.UnsubURL),
		NotifyEmail:            emptyToNil(req.NotifyEmail),
	}
	snd, err := s.store.CreateSend(r.Context(), params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.worker.Start(r.Context(), snd.ID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"send_id":          snd.ID,
		"status":           snd.Status,
		"total_recipients": total,
	})
}

type singleSendReq struct {
	To      string `json:"to"`
	From    string `json:"from"`
	ReplyTo string `json:"reply_to"`
	Subject string `json:"subject"`
	MD      string `json:"md"`
	HTML    string `json:"html"`
	Text    string `json:"text"`
}

func (s *Server) handleSingleSend(w http.ResponseWriter, r *http.Request) {
	var req singleSendReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.To) == "" || strings.TrimSpace(req.Subject) == "" || strings.TrimSpace(req.From) == "" {
		writeError(w, http.StatusBadRequest, "to, subject, and from are required")
		return
	}
	if req.MD == "" && req.HTML == "" {
		writeError(w, http.StatusBadRequest, "either md or html is required")
		return
	}

	var bodyHTML, bodyText string
	var err error
	if req.MD != "" {
		bodyHTML, bodyText, _, err = render.BuildBody(req.MD, "", req.Subject, "")
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	} else {
		htmlWithFooter := render.EnsureUnsubFooterHTML(req.HTML)
		rewritten, _ := render.RewriteVariables(htmlWithFooter)
		bodyHTML = rewritten
		baseText := req.Text
		if baseText == "" {
			baseText = render.HTMLToText(req.HTML)
		}
		rewrittenText, _ := render.RewriteVariables(render.EnsureUnsubFooterText(baseText))
		bodyText = rewrittenText
	}

	to := req.To
	params := store.NewSendParams{
		Type:           models.SendTypeSingle,
		RecipientEmail: &to,
		Subject:        req.Subject,
		FromHeader:     req.From,
		ReplyTo:        emptyToNil(req.ReplyTo),
		BodyMD:         emptyToNil(req.MD),
		BodyHTML:       &bodyHTML,
		BodyText:       &bodyText,
		BatchSize:      1,
		ThrottleMS:     0,
	}
	snd, err := s.store.CreateSend(r.Context(), params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.worker.Start(r.Context(), snd.ID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"send_id": snd.ID,
		"status":  snd.Status,
	})
}

func (s *Server) handleResumeSend(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	snd, err := s.store.GetSend(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "send not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if snd.Status == models.SendStatusRunning {
		writeError(w, http.StatusConflict, "send already running")
		return
	}
	if snd.Status == models.SendStatusCompleted || snd.Status == models.SendStatusCancelled {
		writeError(w, http.StatusConflict, "send already terminal")
		return
	}
	hasInFlight, err := s.store.HasInFlightBatch(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	force := r.URL.Query().Get("force") == "1"
	if hasInFlight && !force {
		writeError(w, http.StatusConflict, "send has in_flight batches from a previous run; retry with ?force=1 (may cause duplicate sends)")
		return
	}
	if err := s.worker.Start(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"send_id": id,
		"status":  "resumed",
	})
}

func (s *Server) handleGetSend(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	snd, err := s.store.GetSend(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "send not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	completedBatches, sent, err := s.store.SendProgress(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	totalBatches := 0
	if snd.BatchSize > 0 && snd.TotalRecipients > 0 {
		totalBatches = (snd.TotalRecipients + snd.BatchSize - 1) / snd.BatchSize
	}
	remaining := snd.TotalRecipients - sent
	if remaining < 0 {
		remaining = 0
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":     snd.ID,
		"status": snd.Status,
		"progress": map[string]any{
			"completed_batches":    completedBatches,
			"total_batches":        totalBatches,
			"sent":                 sent,
			"remaining":            remaining,
			"last_subscription_id": snd.LastSubscriptionID,
		},
		"created_at":   snd.CreatedAt,
		"updated_at":   snd.UpdatedAt,
		"completed_at": snd.CompletedAt,
		"last_error":   snd.LastError,
	})
}

func (s *Server) handleSendStats(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	snd, err := s.store.GetSend(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "send not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_, sent, err := s.store.SendProgress(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	unsub, err := s.store.CountUnsubscribesForSend(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	start := snd.CreatedAt.Add(-24 * time.Hour)
	end := time.Now().Add(24 * time.Hour)
	mr := mailgun.MetricsRequest{
		Start:      start,
		End:        end,
		Resolution: "day",
		Metrics:    []string{"accepted_count", "delivered_count", "failed_count", "opened_count", "clicked_count", "complained_count"},
		Tag:        snd.ID,
	}
	totals := map[string]uint64{}
	if metrics, err := s.mailgun.Metrics(r.Context(), mr); err == nil && metrics != nil {
		for _, item := range metrics.Items {
			for k, v := range item.Metrics {
				totals[k] += v
			}
		}
	} else if err != nil {
		s.log.Warn("mailgun metrics failed", "send_id", snd.ID, "err", err)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":           snd.ID,
		"sent":         sent,
		"delivered":    totals["delivered_count"],
		"failed":       totals["failed_count"],
		"opened":       totals["opened_count"],
		"clicked":      totals["clicked_count"],
		"complained":   totals["complained_count"],
		"unsubscribed": unsub,
	})
}

func emptyToNil(s string) *string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return &s
}
