// SPDX-License-Identifier: AGPL-3.0-or-later
// Package api implements the claw HTTP+WebSocket API server.
// It translates the driver NDJSON protocol to HTTP/WS for claw-console.
package api

import (
	"net/http"

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

	// Middleware chain (applied inside-out): request → CORS → auth → handler
	var handler http.Handler = mux
	handler = authMiddleware(s.Token, handler)
	handler = corsMiddleware(s.CORSOrigins, handler)

	return handler
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
