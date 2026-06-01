package api

import (
	"errors"
	"fmt"
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
	Domain     string `json:"domain"`
	MD         string `json:"md"`
	HTML       string `json:"html"`
	Text       string `json:"text"`
	Template   string `json:"template"`
	BatchSize  int    `json:"batch_size"`
	ThrottleMS int    `json:"throttle_ms"`

	UnsubMode   string `json:"unsub_mode"`
	UnsubRedir  string `json:"unsub_redir"`
	UnsubURL    string `json:"unsub_url"`
	NotifyEmail string `json:"notify_email"`
	TestMode    bool   `json:"test_mode"`
	SendAt      string `json:"send_at"`
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
	sendAt, err := parseSendAt(req.SendAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	list, err := s.store.ResolveList(r.Context(), req.List)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "list not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	listID := list.ID
	sendingDomain := strings.ToLower(strings.TrimSpace(req.Domain))
	if sendingDomain == "" {
		sendingDomain = list.SendingDomain
	}
	if sendingDomain == "" {
		writeError(w, http.StatusBadRequest, `list has no sending_domain configured; pass "domain" to override or fix the list`)
		return
	}

	var bodyHTML, bodyText string
	if req.MD != "" {
		bodyHTML, bodyText, _, err = render.BuildBody(req.MD, req.Template, req.Subject, req.Preheader, true)
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

	// A scheduled bulk send resolves its audience at dispatch (so everyone
	// subscribed up to go-time is included), not at creation. Park it with
	// no max_subscription_id; total here is just a live estimate.
	scheduled := sendAt != nil && sendAt.After(time.Now())
	var maxSubID *int64
	if !scheduled {
		maxSubID = &maxID
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
		SendingDomain:          sendingDomain,
		BatchSize:              req.BatchSize,
		ThrottleMS:             req.ThrottleMS,
		MaxSubscriptionID:      maxSubID,
		TotalRecipients:        total,
		UnsubscribeMode:        mode,
		UnsubscribeRedirectURL: emptyToNil(req.UnsubRedir),
		UnsubscribeExternalURL: emptyToNil(req.UnsubURL),
		NotifyEmail:            emptyToNil(req.NotifyEmail),
		TestMode:               req.TestMode,
		SendAt:                 sendAt,
	}
	snd, err := s.store.CreateSend(r.Context(), params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// A future-dated send is parked for the dispatcher; don't kick it now.
	if snd.Status == models.SendStatusScheduled {
		writeJSON(w, http.StatusAccepted, map[string]any{
			"send_id":          snd.ID,
			"status":           snd.Status,
			"send_at":          snd.SendAt,
			"total_recipients": total,
		})
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
	To        string `json:"to"`
	From      string `json:"from"`
	ReplyTo   string `json:"reply_to"`
	Subject   string `json:"subject"`
	Preheader string `json:"preheader"`
	Company   string `json:"company"`
	List      string `json:"list"`
	Domain    string `json:"domain"`
	MD        string `json:"md"`
	HTML      string `json:"html"`
	Text      string `json:"text"`
	Template  string `json:"template"`
	TestMode  bool   `json:"test_mode"`
	SendAt    string `json:"send_at"`
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
	if strings.TrimSpace(req.Company) == "" {
		writeError(w, http.StatusBadRequest, "company is required (id or slug) to resolve sending domain")
		return
	}
	if req.MD == "" && req.HTML == "" {
		writeError(w, http.StatusBadRequest, "either md or html is required")
		return
	}
	sendAt, err := parseSendAt(req.SendAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	company, err := s.store.ResolveCompany(r.Context(), strings.TrimSpace(req.Company))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "company not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	sendingDomain := strings.ToLower(strings.TrimSpace(req.Domain))
	if sendingDomain == "" {
		sendingDomain = company.SendingDomain
	}
	if sendingDomain == "" {
		writeError(w, http.StatusBadRequest, `company has no sending_domain configured; pass "domain" to override or fix the company`)
		return
	}

	// If the caller tied this transactional send to a list, upsert the
	// recipient's contact + subscription so we can sign a real per-recipient
	// unsubscribe token at send time. When no list is given the send is
	// pure transactional with no opt-out — we skip auto-injecting an unsub
	// footer below.
	var (
		listIDPtr   *string
		subscriptionID int64
	)
	if strings.TrimSpace(req.List) != "" {
		list, err := s.store.ResolveList(r.Context(), strings.TrimSpace(req.List))
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusNotFound, "list not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		listIDPtr = &list.ID
		contact, err := s.store.UpsertContact(r.Context(), req.To, nil)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		sub, err := s.store.UpsertSubscription(r.Context(), list.ID, contact.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		subscriptionID = sub.ID
	}

	var bodyHTML, bodyText string
	if req.MD != "" {
		bodyHTML, bodyText, _, err = render.BuildBody(req.MD, req.Template, req.Subject, req.Preheader, subscriptionID > 0)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	} else {
		htmlSrc := req.HTML
		textSrc := req.Text
		if textSrc == "" {
			textSrc = render.HTMLToText(req.HTML)
		}
		if subscriptionID > 0 {
			htmlSrc = render.EnsureUnsubFooterHTML(htmlSrc)
			textSrc = render.EnsureUnsubFooterText(textSrc)
		}
		rewritten, _ := render.RewriteVariables(htmlSrc)
		bodyHTML = rewritten
		rewrittenText, _ := render.RewriteVariables(textSrc)
		bodyText = rewrittenText
	}

	to := req.To
	params := store.NewSendParams{
		Type:               models.SendTypeSingle,
		ListID:             listIDPtr,
		RecipientEmail:     &to,
		Subject:            req.Subject,
		FromHeader:         req.From,
		ReplyTo:            emptyToNil(req.ReplyTo),
		BodyMD:             emptyToNil(req.MD),
		BodyHTML:           &bodyHTML,
		BodyText:           &bodyText,
		SendingDomain:      sendingDomain,
		BatchSize:          1,
		ThrottleMS:         0,
		TestMode:           req.TestMode,
		LastSubscriptionID: subscriptionID,
		SendAt:             sendAt,
	}
	snd, err := s.store.CreateSend(r.Context(), params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if snd.Status == models.SendStatusScheduled {
		writeJSON(w, http.StatusAccepted, map[string]any{
			"send_id": snd.ID,
			"status":  snd.Status,
			"send_at": snd.SendAt,
		})
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

func (s *Server) handleCancelSend(w http.ResponseWriter, r *http.Request) {
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
	switch snd.Status {
	case models.SendStatusRunning:
		writeError(w, http.StatusConflict, "send is already running; cannot cancel an in-flight send")
		return
	case models.SendStatusCompleted, models.SendStatusFailed, models.SendStatusCancelled:
		writeError(w, http.StatusConflict, "send is already "+string(snd.Status))
		return
	}
	cancelled, err := s.store.CancelScheduledSend(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !cancelled {
		// Lost the race with the dispatcher between the status read above
		// and the guarded update — it just started running.
		writeError(w, http.StatusConflict, "send started before it could be cancelled")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"send_id": id,
		"status":  models.SendStatusCancelled,
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
		"send_at":      snd.SendAt,
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

	st, statsErr := s.store.GetSendStats(r.Context(), id)
	if statsErr != nil && !errors.Is(statsErr, store.ErrNotFound) {
		writeError(w, http.StatusInternalServerError, statsErr.Error())
		return
	}

	// Stable path: row exists and has at least one Mailgun fetch (or is_final).
	if st != nil && (st.IsFinal || st.LastFetchedAt != nil) {
		writeJSON(w, http.StatusOK, map[string]any{
			"id":              snd.ID,
			"sent":            st.Sent,
			"delivered":       st.Delivered,
			"opened":          st.Opened,
			"clicked":         st.Clicked,
			"failed":          st.Failed,
			"complained":      st.Complained,
			"unsubscribed":    st.Unsubscribed,
			"is_final":        st.IsFinal,
			"last_fetched_at": st.LastFetchedAt,
			"source":          "send_stats",
		})
		return
	}

	// Fallback path: no Mailgun poll yet (send still running, or completed
	// less than ~15 minutes ago). Compute live numbers — but always use
	// the DB-tracked unsubscribed count, never Mailgun's.
	totals, mgErr := s.mailgun.PerSendMetrics(r.Context(), snd.ID, snd.CreatedAt)
	if mgErr != nil {
		s.log.Warn("mailgun metrics failed", "send_id", snd.ID, "err", mgErr)
		totals = &mailgun.PerSendTotals{}
	}
	unsub := uint64(0)
	if st != nil {
		unsub = st.Unsubscribed
	} else {
		n, err := s.store.CountUnsubscribesForSend(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		unsub = uint64(n)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":           snd.ID,
		"sent":         totals.Sent,
		"delivered":    totals.Delivered,
		"opened":       totals.Opened,
		"clicked":      totals.Clicked,
		"failed":       totals.Failed,
		"complained":   totals.Complained,
		"unsubscribed": unsub,
		"is_final":     false,
		"source":       "mailgun_live",
	})
}

func emptyToNil(s string) *string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return &s
}

// parseSendAt parses an optional RFC3339 schedule time. Empty means "send
// now" (nil). A past timestamp is accepted and also sends now — the store
// only parks the send when the time is genuinely in the future, so callers
// don't get tripped up by minor clock skew.
func parseSendAt(s string) (*time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil, fmt.Errorf("send_at must be an RFC3339 timestamp (e.g. 2026-06-01T09:00:00Z): %v", err)
	}
	return &t, nil
}
