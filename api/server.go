// SPDX-License-Identifier: AGPL-3.0-or-later
// Package api implements the claw HTTP+WebSocket API server.
// It translates the driver NDJSON protocol to HTTP/WS for claw-console.
package api

import (
	"io/fs"
	"net/http"
	"strings"

	"github.com/kenbolton/claw/driver"
)

// Server is the claw API server.
type Server struct {
	Drivers     []*driver.Driver
	SourceDir   string
	Token       string
	Bind        string
	Port        int
	CORSOrigins []string
	ConsoleFS   fs.FS // embedded console static assets (nil = no console)
}

// NewServeMux builds the HTTP handler with all routes and middleware.
func (s *Server) NewServeMux() http.Handler {
	mux := http.NewServeMux()

	// REST endpoints
	mux.HandleFunc("GET /api/v1/archs", s.handleArchs)
	mux.HandleFunc("GET /api/v1/ps", s.handlePs)
	mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	mux.HandleFunc("GET /api/v1/groups", s.handleGroups)
	mux.HandleFunc("GET /api/v1/sessions", s.handleSessions)

	// WebSocket endpoints
	mux.HandleFunc("GET /ws/watch/{group}", s.handleWatchWS)
	mux.HandleFunc("GET /ws/agent/{group}", s.handleAgentWS)
	mux.HandleFunc("GET /ws/health", s.handleHealthWS)
	mux.HandleFunc("GET /ws/logs/{group}", s.handleLogsWS)

	// Serve embedded console as SPA catch-all (if enabled)
	if s.ConsoleFS != nil {
		mux.HandleFunc("GET /{path...}", s.spaHandler())
	}

	// Middleware chain (applied inside-out): request → CORS → auth → handler
	var handler http.Handler = mux
	handler = authMiddleware(s.Token, handler)
	handler = corsMiddleware(s.CORSOrigins, handler)

	return handler
}

// spaHandler serves static files, falling back to index.html for client-side routes.
func (s *Server) spaHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/ws/") {
			http.NotFound(w, r)
			return
		}

		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "index.html"
		}

		data, err := fs.ReadFile(s.ConsoleFS, name)
		if err != nil {
			// Fall back to index.html for client-side routing
			data, err = fs.ReadFile(s.ConsoleFS, "index.html")
			if err != nil {
				http.NotFound(w, r)
				return
			}
			name = "index.html"
		}

		// Set content type from extension
		switch {
		case strings.HasSuffix(name, ".html"):
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
		case strings.HasSuffix(name, ".js"):
			w.Header().Set("Content-Type", "application/javascript")
		case strings.HasSuffix(name, ".css"):
			w.Header().Set("Content-Type", "text/css")
		case strings.HasSuffix(name, ".svg"):
			w.Header().Set("Content-Type", "image/svg+xml")
		case strings.HasSuffix(name, ".json"):
			w.Header().Set("Content-Type", "application/json")
		case strings.HasSuffix(name, ".png"):
			w.Header().Set("Content-Type", "image/png")
		case strings.HasSuffix(name, ".ico"):
			w.Header().Set("Content-Type", "image/x-icon")
		}

		// Hashed assets are immutable
		if strings.HasPrefix(name, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	}
}

// locateDriver finds a driver by arch from the server's driver list.
func (s *Server) locateDriver(arch string) *driver.Driver {
	for _, d := range s.Drivers {
		if d.Arch == arch {
			return d
		}
	}
	return nil
}

// filterDrivers returns drivers matching the arch filter, or all if arch is empty.
func (s *Server) filterDrivers(arch string) []*driver.Driver {
	if arch == "" {
		return s.Drivers
	}
	for _, d := range s.Drivers {
		if d.Arch == arch {
			return []*driver.Driver{d}
		}
	}
	return nil
}
