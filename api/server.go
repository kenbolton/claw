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
		mux.Handle("/", s.spaHandler(http.FileServer(http.FS(s.ConsoleFS))))
	}

	// Middleware chain (applied inside-out): request → CORS → auth → handler
	var handler http.Handler = mux
	handler = authMiddleware(s.Token, handler)
	handler = corsMiddleware(s.CORSOrigins, handler)

	return handler
}

// spaHandler serves static files, falling back to index.html for client-side routes.
func (s *Server) spaHandler(fileServer http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// API and WebSocket paths are never caught here (registered with
		// explicit method+path patterns which take precedence in Go 1.22+).
		// But guard defensively.
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/ws/") {
			http.NotFound(w, r)
			return
		}

		// Try serving the exact file (JS, CSS, images, etc.)
		path := r.URL.Path
		if path == "/" {
			path = "/index.html"
		}
		if _, err := fs.Stat(s.ConsoleFS, strings.TrimPrefix(path, "/")); err == nil {
			fileServer.ServeHTTP(w, r)
			return
		}

		// Fall back to index.html for client-side routing
		r.URL.Path = "/index.html"
		fileServer.ServeHTTP(w, r)
	})
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
