// SPDX-License-Identifier: AGPL-3.0-or-later
package api

import (
	"net/http"
	"strings"
)

// authMiddleware enforces bearer token authentication when token is non-empty.
func authMiddleware(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// WebSocket endpoints accept token as query param.
		if t := r.URL.Query().Get("token"); t == token {
			next.ServeHTTP(w, r)
			return
		}
		auth := r.Header.Get("Authorization")
		if auth == "Bearer "+token {
			next.ServeHTTP(w, r)
			return
		}
		writeError(w, http.StatusUnauthorized, "AUTH_REQUIRED", "valid bearer token required")
	})
}

// corsMiddleware handles CORS headers and preflight requests.
func corsMiddleware(extraOrigins []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && isAllowedOrigin(origin, extraOrigins) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isAllowedOrigin checks if the origin is allowed by default localhost rule or extra origins.
func isAllowedOrigin(origin string, extraOrigins []string) bool {
	if strings.HasPrefix(origin, "http://localhost:") || origin == "http://localhost" {
		return true
	}
	for _, o := range extraOrigins {
		if origin == o {
			return true
		}
	}
	return false
}
