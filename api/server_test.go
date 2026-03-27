// SPDX-License-Identifier: AGPL-3.0-or-later
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kenbolton/claw/driver"
)

func testServer(drivers ...*driver.Driver) *Server {
	return &Server{
		Drivers:     drivers,
		SourceDir:   "",
		Token:       "",
		Bind:        "127.0.0.1",
		Port:        7474,
		CORSOrigins: nil,
	}
}

func TestHandleArchs(t *testing.T) {
	srv := testServer(
		&driver.Driver{Arch: "nanoclaw", ArchVersion: "1.2.3", DriverVersion: "0.1.0", DriverType: "local", Path: "/usr/bin/claw-driver-nanoclaw"},
		&driver.Driver{Arch: "zepto", ArchVersion: "0.5.0", DriverVersion: "0.1.0", DriverType: "local", Path: "/usr/bin/claw-driver-zepto"},
	)

	handler := srv.NewServeMux()
	req := httptest.NewRequest("GET", "/api/v1/archs", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp struct {
		Drivers []struct {
			Arch          string `json:"arch"`
			ArchVersion   string `json:"arch_version"`
			DriverVersion string `json:"driver_version"`
			DriverType    string `json:"driver_type"`
			Path          string `json:"path"`
		} `json:"drivers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(resp.Drivers) != 2 {
		t.Fatalf("expected 2 drivers, got %d", len(resp.Drivers))
	}
	if resp.Drivers[0].Arch != "nanoclaw" {
		t.Errorf("expected nanoclaw, got %s", resp.Drivers[0].Arch)
	}
	if resp.Drivers[1].Arch != "zepto" {
		t.Errorf("expected zepto, got %s", resp.Drivers[1].Arch)
	}
}

func TestHandleArchsEmpty(t *testing.T) {
	srv := testServer()
	handler := srv.NewServeMux()
	req := httptest.NewRequest("GET", "/api/v1/archs", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp struct {
		Drivers []interface{} `json:"drivers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(resp.Drivers) != 0 {
		t.Fatalf("expected 0 drivers, got %d", len(resp.Drivers))
	}
}

func TestContentTypeJSON(t *testing.T) {
	srv := testServer()
	handler := srv.NewServeMux()
	req := httptest.NewRequest("GET", "/api/v1/archs", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("expected application/json, got %s", ct)
	}
}

func TestAuthMiddleware_NoToken(t *testing.T) {
	srv := testServer()
	handler := srv.NewServeMux()

	req := httptest.NewRequest("GET", "/api/v1/archs", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 without token config, got %d", rec.Code)
	}
}

func TestAuthMiddleware_WithToken_Reject(t *testing.T) {
	srv := testServer()
	srv.Token = "secret123"
	handler := srv.NewServeMux()

	req := httptest.NewRequest("GET", "/api/v1/archs", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestAuthMiddleware_WithToken_BearerHeader(t *testing.T) {
	srv := testServer()
	srv.Token = "secret123"
	handler := srv.NewServeMux()

	req := httptest.NewRequest("GET", "/api/v1/archs", nil)
	req.Header.Set("Authorization", "Bearer secret123")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with valid bearer, got %d", rec.Code)
	}
}

func TestAuthMiddleware_WithToken_QueryParam(t *testing.T) {
	srv := testServer()
	srv.Token = "secret123"
	handler := srv.NewServeMux()

	req := httptest.NewRequest("GET", "/api/v1/archs?token=secret123", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with valid query token, got %d", rec.Code)
	}
}

func TestAuthMiddleware_WithToken_WrongToken(t *testing.T) {
	srv := testServer()
	srv.Token = "secret123"
	handler := srv.NewServeMux()

	req := httptest.NewRequest("GET", "/api/v1/archs", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with wrong token, got %d", rec.Code)
	}
}

func TestCORSMiddleware_LocalhostAllowed(t *testing.T) {
	srv := testServer()
	handler := srv.NewServeMux()

	req := httptest.NewRequest("GET", "/api/v1/archs", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if v := rec.Header().Get("Access-Control-Allow-Origin"); v != "http://localhost:3000" {
		t.Errorf("expected localhost origin in CORS, got %q", v)
	}
}

func TestCORSMiddleware_UnknownOriginBlocked(t *testing.T) {
	srv := testServer()
	handler := srv.NewServeMux()

	req := httptest.NewRequest("GET", "/api/v1/archs", nil)
	req.Header.Set("Origin", "http://evil.com")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if v := rec.Header().Get("Access-Control-Allow-Origin"); v != "" {
		t.Errorf("expected no CORS header for unknown origin, got %q", v)
	}
}

func TestCORSMiddleware_ExtraOriginAllowed(t *testing.T) {
	srv := testServer()
	srv.CORSOrigins = []string{"https://console.example.com"}
	handler := srv.NewServeMux()

	req := httptest.NewRequest("GET", "/api/v1/archs", nil)
	req.Header.Set("Origin", "https://console.example.com")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if v := rec.Header().Get("Access-Control-Allow-Origin"); v != "https://console.example.com" {
		t.Errorf("expected extra origin in CORS, got %q", v)
	}
}

func TestCORSMiddleware_Preflight(t *testing.T) {
	srv := testServer()
	handler := srv.NewServeMux()

	req := httptest.NewRequest("OPTIONS", "/api/v1/archs", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for preflight, got %d", rec.Code)
	}
	if v := rec.Header().Get("Access-Control-Allow-Headers"); v == "" {
		t.Error("expected Access-Control-Allow-Headers in preflight response")
	}
}

func TestFilterDrivers(t *testing.T) {
	srv := testServer(
		&driver.Driver{Arch: "nanoclaw"},
		&driver.Driver{Arch: "zepto"},
	)

	all := srv.filterDrivers("")
	if len(all) != 2 {
		t.Errorf("expected 2, got %d", len(all))
	}

	nano := srv.filterDrivers("nanoclaw")
	if len(nano) != 1 || nano[0].Arch != "nanoclaw" {
		t.Errorf("expected [nanoclaw], got %v", nano)
	}

	none := srv.filterDrivers("unknown")
	if len(none) != 0 {
		t.Errorf("expected empty, got %d", len(none))
	}
}

func TestLocateDriver(t *testing.T) {
	srv := testServer(
		&driver.Driver{Arch: "nanoclaw"},
		&driver.Driver{Arch: "zepto"},
	)

	d := srv.locateDriver("zepto")
	if d == nil || d.Arch != "zepto" {
		t.Error("expected to find zepto driver")
	}

	d = srv.locateDriver("unknown")
	if d != nil {
		t.Error("expected nil for unknown driver")
	}
}

func TestPsNoDrivers(t *testing.T) {
	srv := testServer()
	handler := srv.NewServeMux()

	req := httptest.NewRequest("GET", "/api/v1/ps?arch=unknown", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestHealthNoDrivers(t *testing.T) {
	srv := testServer()
	handler := srv.NewServeMux()

	req := httptest.NewRequest("GET", "/api/v1/health?arch=unknown", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestSessionsMissingParams(t *testing.T) {
	srv := testServer(&driver.Driver{Arch: "nanoclaw"})
	handler := srv.NewServeMux()

	// Missing both arch and group
	req := httptest.NewRequest("GET", "/api/v1/sessions", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}

	// Missing group
	req = httptest.NewRequest("GET", "/api/v1/sessions?arch=nanoclaw", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestSessionsArchNotFound(t *testing.T) {
	srv := testServer(&driver.Driver{Arch: "nanoclaw"})
	handler := srv.NewServeMux()

	req := httptest.NewRequest("GET", "/api/v1/sessions?arch=unknown&group=main", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestGroupsNoDrivers(t *testing.T) {
	srv := testServer()
	handler := srv.NewServeMux()

	req := httptest.NewRequest("GET", "/api/v1/groups?arch=unknown", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}
