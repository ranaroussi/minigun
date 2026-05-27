package api

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

func (s *Server) bearerAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.APIToken == "" {
			next.ServeHTTP(w, r)
			return
		}
		if isAuthExempt(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		h := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(h, prefix) {
			writeError(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		got := h[len(prefix):]
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.cfg.APIToken)) != 1 {
			writeError(w, http.StatusUnauthorized, "invalid bearer token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isAuthExempt(path string) bool {
	if path == "/healthz" {
		return true
	}
	if strings.HasPrefix(path, "/u/") {
		return true
	}
	if strings.HasPrefix(path, "/manage/") {
		return true
	}
	// Webhooks authenticate via Mailgun HMAC signature inside the
	// handler instead of the operator's bearer token.
	if strings.HasPrefix(path, "/webhooks/") {
		return true
	}
	return false
}
