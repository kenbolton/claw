// SPDX-License-Identifier: AGPL-3.0-or-later
package api

import (
	"net/http"
	"strconv"

	"github.com/kenbolton/claw/driver"
)

// handleUsage serves GET /api/v1/usage — returns per-run token usage data
// aggregated across all architecture drivers.
//
// Each row represents one agent run and includes input/output/cache token
// counts, wall-clock duration, and a server-side estimated cost in USD.
//
// Query parameters (all optional):
//
//	arch         — restrict to a single architecture (e.g. "nanoclaw")
//	group_folder — filter by group folder name
//	since        — ISO 8601 timestamp; only return runs completed after this time
//	limit        — max rows per driver (default 500)
//
// Response JSON:
//
//	{
//	  "rows":   [ { arch, group_folder, chat_jid, completed_at, duration_ms,
//	                input_tokens, output_tokens, cache_read_input_tokens,
//	                cache_creation_input_tokens, estimated_cost_usd } ],
//	  "totals": { runs, input_tokens, output_tokens, cache_read_input_tokens,
//	              cache_creation_input_tokens, estimated_cost_usd }
//	}
//
// Cost estimates use hardcoded Claude Sonnet 4 rates (see estimateCost).
// Actual invoiced amounts may differ due to commitment discounts, model
// selection, or pricing changes.
func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	arch := r.URL.Query().Get("arch")
	groupFolder := r.URL.Query().Get("group_folder")
	since := r.URL.Query().Get("since")
	limit := 500
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 {
			limit = v
		}
	}

	drivers := s.filterDrivers(arch)
	if len(drivers) == 0 {
		writeError(w, http.StatusNotFound, "ARCH_NOT_FOUND", "no driver found for arch")
		return
	}

	results := fanOut(drivers, func(d *driver.Driver) map[string]interface{} {
		req := map[string]interface{}{
			"type":       "usage_request",
			"source_dir": s.SourceDir,
			"limit":      limit,
		}
		if groupFolder != "" {
			req["group_folder"] = groupFolder
		}
		if since != "" {
			req["since"] = since
		}
		return req
	})

	// usageRow is the JSON shape for a single agent run in the response.
	// Token fields mirror the Anthropic API usage object; estimated_cost_usd
	// is computed server-side via estimateCost().
	type usageRow struct {
		Arch                     string  `json:"arch"`                        // architecture that ran this agent (e.g. "nanoclaw")
		GroupFolder              string  `json:"group_folder"`                // group folder name (e.g. "main", "dev")
		ChatJID                  string  `json:"chat_jid"`                    // chat JID that triggered the run
		CompletedAt              string  `json:"completed_at"`                // ISO 8601 completion timestamp
		DurationMs               int     `json:"duration_ms"`                 // wall-clock duration of the agent run
		InputTokens              int     `json:"input_tokens"`                // non-cached input tokens consumed
		OutputTokens             int     `json:"output_tokens"`               // output tokens generated
		CacheReadInputTokens     int     `json:"cache_read_input_tokens"`     // input tokens served from prompt cache
		CacheCreationInputTokens int     `json:"cache_creation_input_tokens"` // input tokens written to prompt cache
		EstimatedCostUSD         float64 `json:"estimated_cost_usd"`          // estimated cost based on published rates
	}

	var rows []usageRow

	for _, res := range results {
		if res.Err != nil {
			continue
		}
		for _, msg := range res.Messages {
			msgType, _ := msg["type"].(string)
			if msgType == "usage_row" {
				row := usageRow{
					Arch:        res.Driver.Arch,
					GroupFolder: msgStr(msg, "group_folder"),
					ChatJID:     msgStr(msg, "chat_jid"),
					CompletedAt: msgStr(msg, "completed_at"),
				}
				if v, ok := msg["duration_ms"].(float64); ok {
					row.DurationMs = int(v)
				}
				if v, ok := msg["input_tokens"].(float64); ok {
					row.InputTokens = int(v)
				}
				if v, ok := msg["output_tokens"].(float64); ok {
					row.OutputTokens = int(v)
				}
				if v, ok := msg["cache_read_input_tokens"].(float64); ok {
					row.CacheReadInputTokens = int(v)
				}
				if v, ok := msg["cache_creation_input_tokens"].(float64); ok {
					row.CacheCreationInputTokens = int(v)
				}
				row.EstimatedCostUSD = estimateCost(
					row.InputTokens, row.OutputTokens,
					row.CacheReadInputTokens, row.CacheCreationInputTokens,
				)
				rows = append(rows, row)
			}
		}
	}

	if rows == nil {
		rows = []usageRow{}
	}

	// Compute totals
	var totalInput, totalOutput, totalCacheRead, totalCacheCreation int
	var totalCost float64
	for _, row := range rows {
		totalInput += row.InputTokens
		totalOutput += row.OutputTokens
		totalCacheRead += row.CacheReadInputTokens
		totalCacheCreation += row.CacheCreationInputTokens
		totalCost += row.EstimatedCostUSD
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"rows": rows,
		"totals": map[string]interface{}{
			"runs":                        len(rows),
			"input_tokens":                totalInput,
			"output_tokens":               totalOutput,
			"cache_read_input_tokens":     totalCacheRead,
			"cache_creation_input_tokens": totalCacheCreation,
			"estimated_cost_usd":          totalCost,
		},
	})
}

// estimateCost calculates the estimated cost in USD for a single agent run
// based on Claude Sonnet 4 published pricing (as of March 2025).
//
// Rates per 1M tokens:
//
//	Input:          $3.00   — standard non-cached input tokens
//	Output:         $15.00  — generated output tokens
//	Cache read:     $0.30   — 10% of the input rate; tokens served from prompt cache
//	Cache creation: $3.75   — 125% of the input rate; tokens written to prompt cache
//
// These are estimates. Actual invoiced amounts may differ due to commitment
// pricing, batch API discounts, or rate changes. If the model changes (e.g. to
// Opus or Haiku), these rates will need updating — a future enhancement could
// store the model per run and look up rates dynamically.
func estimateCost(input, output, cacheRead, cacheCreation int) float64 {
	const (
		inputRate         = 3.00 / 1_000_000
		outputRate        = 15.00 / 1_000_000
		cacheReadRate     = 0.30 / 1_000_000
		cacheCreationRate = 3.75 / 1_000_000
	)
	return float64(input)*inputRate +
		float64(output)*outputRate +
		float64(cacheRead)*cacheReadRate +
		float64(cacheCreation)*cacheCreationRate
}
