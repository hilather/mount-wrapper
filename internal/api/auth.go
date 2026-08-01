package api

import (
	"net/http"
	"strings"
)

// checkToken validates optional Bearer / query-token auth for /api/* routes.
// When token is empty, all requests are allowed (parity with upstream web.py).
func checkToken(r *http.Request, token string) bool {
	if token == "" {
		return true
	}
	auth := r.Header.Get("Authorization")
	if auth == "Bearer "+token {
		return true
	}
	// Also allow ?token= for simple browser testing of GET APIs.
	if q := r.URL.Query().Get("token"); q == token {
		return true
	}
	// Tolerate "Bearer <token>" with extra spaces.
	if strings.HasPrefix(auth, "Bearer ") && strings.TrimSpace(strings.TrimPrefix(auth, "Bearer ")) == token {
		return true
	}
	return false
}

// withAPIAuth wraps an API handler with token checks (401 when configured token fails).
func withAPIAuth(token string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !checkToken(r, token) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"error": "unauthorized",
				"code":  "UNAUTHORIZED",
			})
			return
		}
		next(w, r)
	}
}
