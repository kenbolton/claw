// SPDX-License-Identifier: AGPL-3.0-or-later
package api

// Tests for the usage API endpoint and cost estimation logic.
//
// Test matrix:
//   - TestEstimateCost           — verifies 1M tokens per category totals $22.05
//   - TestEstimateCostZero       — zero tokens → $0
//   - TestEstimateCostTypicalRun — realistic token mix matches manual calculation
//   - TestUsageNoDrivers         — GET /api/v1/usage with unknown arch → 404

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEstimateCost(t *testing.T) {
	// 1M input tokens at $3/M = $3.00
	// 1M output tokens at $15/M = $15.00
	// 1M cache read tokens at $0.30/M = $0.30
	// 1M cache creation tokens at $3.75/M = $3.75
	// Total = $22.05
	cost := estimateCost(1_000_000, 1_000_000, 1_000_000, 1_000_000)
	expected := 22.05
	if cost < expected-0.01 || cost > expected+0.01 {
		t.Errorf("expected ~$%.2f, got $%.4f", expected, cost)
	}
}

func TestEstimateCostZero(t *testing.T) {
	cost := estimateCost(0, 0, 0, 0)
	if cost != 0 {
		t.Errorf("expected $0, got $%.4f", cost)
	}
}

func TestEstimateCostTypicalRun(t *testing.T) {
	// Typical run: 10K input, 2K output, 8K cache read, 2K cache write
	cost := estimateCost(10_000, 2_000, 8_000, 2_000)
	// 10000 * 3/1M = 0.03
	// 2000 * 15/1M = 0.03
	// 8000 * 0.3/1M = 0.0024
	// 2000 * 3.75/1M = 0.0075
	expected := 0.03 + 0.03 + 0.0024 + 0.0075
	if cost < expected-0.0001 || cost > expected+0.0001 {
		t.Errorf("expected ~$%.4f, got $%.4f", expected, cost)
	}
}

func TestUsageNoDrivers(t *testing.T) {
	srv := testServer()
	handler := srv.NewServeMux()

	req := httptest.NewRequest("GET", "/api/v1/usage?arch=unknown", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}
