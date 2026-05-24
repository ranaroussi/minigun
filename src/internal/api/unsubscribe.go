package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/ranaroussi/minigun/internal/models"
	"github.com/ranaroussi/minigun/internal/store"
	"github.com/ranaroussi/minigun/internal/tmpl"
	"github.com/ranaroussi/minigun/internal/token"
)

func (s *Server) handleUnsubscribeGet(w http.ResponseWriter, r *http.Request) {
	tokenStr := chi.URLParam(r, "token")
	t, err := token.Verify(s.cfg.HMACSecret, tokenStr)
	if err != nil {
		s.renderUnsubPage(w, tmpl.UnsubscribeData{Error: "Invalid or expired unsubscribe link."})
		return
	}
	snd, err := s.store.GetSend(r.Context(), t.SendID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.renderUnsubPage(w, tmpl.UnsubscribeData{Done: true})
			return
		}
		s.renderUnsubPageStatus(w, http.StatusInternalServerError, tmpl.UnsubscribeData{Error: "Something went wrong. Please try again."})
		return
	}
	sub, err := s.store.GetSubscriptionByID(r.Context(), t.SubscriptionID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.renderUnsubPage(w, tmpl.UnsubscribeData{Done: true})
			return
		}
		s.renderUnsubPageStatus(w, http.StatusInternalServerError, tmpl.UnsubscribeData{Error: "Something went wrong. Please try again."})
		return
	}
	contact, err := s.store.GetContactByID(r.Context(), sub.ContactID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.renderUnsubPage(w, tmpl.UnsubscribeData{Done: true})
			return
		}
		s.renderUnsubPageStatus(w, http.StatusInternalServerError, tmpl.UnsubscribeData{Error: "Something went wrong. Please try again."})
		return
	}
	listName := ""
	if snd.ListID != nil {
		if l, err := s.store.GetListByID(r.Context(), *snd.ListID); err == nil {
			listName = l.Name
		}
	}

	if !sub.Subscribed {
		s.renderUnsubPage(w, tmpl.UnsubscribeData{
			Done:     true,
			Email:    contact.Email,
			ListName: listName,
		})
		return
	}

	s.renderUnsubPage(w, tmpl.UnsubscribeData{
		Token:            tokenStr,
		Email:            contact.Email,
		ListName:         listName,
		TurnstileSiteKey: s.cfg.TurnstileSiteKey,
	})
}

func (s *Server) handleUnsubscribePost(w http.ResponseWriter, r *http.Request) {
	tokenStr := chi.URLParam(r, "token")
	if err := r.ParseForm(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	oneClick := isRFC8058OneClick(r)

	t, err := token.Verify(s.cfg.HMACSecret, tokenStr)
	if err != nil {
		if oneClick {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		s.renderUnsubPage(w, tmpl.UnsubscribeData{Error: "Invalid or expired unsubscribe link."})
		return
	}

	if !oneClick && s.cfg.TurnstileSiteKey != "" {
		captchaToken := r.FormValue("cf-turnstile-response")
		ip := clientIP(r)
		res, err := s.turnstile.Verify(r.Context(), captchaToken, ip)
		if err != nil || res == nil || !res.Success {
			s.renderUnsubPage(w, tmpl.UnsubscribeData{
				Token: tokenStr,
				Error: "Bot challenge failed. Please try again.",
				TurnstileSiteKey: s.cfg.TurnstileSiteKey,
			})
			return
		}
	}

	snd, err := s.store.GetSend(r.Context(), t.SendID)
	if err != nil {
		if oneClick {
			w.WriteHeader(http.StatusOK)
			return
		}
		if errors.Is(err, store.ErrNotFound) {
			s.renderUnsubPage(w, tmpl.UnsubscribeData{Done: true})
			return
		}
		s.renderUnsubPageStatus(w, http.StatusInternalServerError, tmpl.UnsubscribeData{Error: "Something went wrong. Please try again."})
		return
	}

	sub, err := s.unsubAndRecord(r.Context(), t, snd)
	if err != nil {
		if oneClick {
			w.WriteHeader(http.StatusOK)
			return
		}
		s.renderUnsubPage(w, tmpl.UnsubscribeData{Done: true})
		return
	}

	if oneClick {
		w.WriteHeader(http.StatusOK)
		return
	}

	switch snd.UnsubscribeMode {
	case models.UnsubModeRedirect:
		if snd.UnsubscribeRedirectURL != nil {
			http.Redirect(w, r, withUnsubQuery(*snd.UnsubscribeRedirectURL, sub, snd), http.StatusFound)
			return
		}
	case models.UnsubModeExternal:
		if snd.UnsubscribeExternalURL != nil {
			http.Redirect(w, r, withUnsubQuery(*snd.UnsubscribeExternalURL, sub, snd), http.StatusFound)
			return
		}
	}

	listName := ""
	email := ""
	if c, err := s.store.GetContactByID(r.Context(), sub.ContactID); err == nil {
		email = c.Email
	}
	if snd.ListID != nil {
		if l, err := s.store.GetListByID(r.Context(), *snd.ListID); err == nil {
			listName = l.Name
		}
	}
	s.renderUnsubPage(w, tmpl.UnsubscribeData{
		Done:     true,
		Email:    email,
		ListName: listName,
	})
}

func (s *Server) unsubAndRecord(ctx context.Context, t *token.Unsubscribe, snd *models.Send) (*models.Subscription, error) {
	sub, err := s.store.GetSubscriptionByID(ctx, t.SubscriptionID)
	if err != nil {
		return nil, err
	}
	if sub.Subscribed {
		updated, err := s.store.UnsubscribeSubscription(ctx, sub.ListID, sub.ContactID)
		if err != nil {
			return nil, err
		}
		sub = updated
	}
	contact, err := s.store.GetContactByID(ctx, sub.ContactID)
	if err == nil {
		_, _ = s.store.RecordUnsubscribeEvent(ctx, &snd.ID, sub, contact.Email)
	}
	return sub, nil
}

func (s *Server) renderUnsubPage(w http.ResponseWriter, data tmpl.UnsubscribeData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if data.Error != "" {
		w.WriteHeader(http.StatusBadRequest)
	}
	if err := tmpl.Unsubscribe.Execute(w, data); err != nil {
		s.log.Error("render unsub page", "err", err)
	}
}

func (s *Server) renderUnsubPageStatus(w http.ResponseWriter, status int, data tmpl.UnsubscribeData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := tmpl.Unsubscribe.Execute(w, data); err != nil {
		s.log.Error("render unsub page", "err", err)
	}
}

func isRFC8058OneClick(r *http.Request) bool {
	if v := r.FormValue("List-Unsubscribe"); v == "One-Click" {
		return true
	}
	return false
}

func clientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
		parts := strings.Split(ip, ",")
		return strings.TrimSpace(parts[0])
	}
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	addr := r.RemoteAddr
	if i := strings.LastIndex(addr, ":"); i > 0 {
		return addr[:i]
	}
	return addr
}

func withUnsubQuery(base string, sub *models.Subscription, snd *models.Send) string {
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	q := u.Query()
	if snd.ListID != nil {
		q.Set("list", *snd.ListID)
	}
	q.Set("subscription_id", itoa(sub.ID))
	u.RawQuery = q.Encode()
	return u.String()
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

var _ = errors.New
