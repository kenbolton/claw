// SPDX-License-Identifier: AGPL-3.0-or-later
package main

// handleUsage processes a usage_request message by querying the run_usage table
// in nanoclaw's SQLite database and streaming the results as NDJSON.
//
// The run_usage table is populated by nanoclaw's index.ts after each agent run,
// recording per-run token counts (input, output, cache read, cache creation)
// along with the group, chat JID, completion timestamp, and wall-clock duration.
//
// Request fields (all optional except type):
//
//	source_dir   — path to the nanoclaw installation (default: auto-detected)
//	group_folder — filter results to a single group folder
//	since        — ISO 8601 timestamp; only return rows with completed_at >= since
//	limit        — max rows to return (default 500)
//
// Response: streams zero or more {"type": "usage_row", ...} messages followed
// by a single {"type": "usage_complete"} terminator. If the run_usage table does
// not yet exist (older database), returns usage_complete immediately with no rows.
func handleUsage(msg map[string]interface{}) {
	sourceDir, _ := msg["source_dir"].(string)
	if sourceDir == "" {
		sourceDir = findSourceDir()
	}
	groupFolder, _ := msg["group_folder"].(string)
	since, _ := msg["since"].(string)
	limit := 500
	if v, ok := msg["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}

	db, err := openDB(sourceDir)
	if err != nil {
		writeError("DB_ERROR", err.Error())
		return
	}
	defer func() { _ = db.Close() }()

	// Build query with optional filters
	query := `SELECT id, group_folder, chat_jid, completed_at, duration_ms,
		input_tokens, output_tokens, cache_read_input_tokens, cache_creation_input_tokens
		FROM run_usage WHERE 1=1`
	var args []interface{}

	if groupFolder != "" {
		query += " AND group_folder = ?"
		args = append(args, groupFolder)
	}
	if since != "" {
		query += " AND completed_at >= ?"
		args = append(args, since)
	}
	query += " ORDER BY completed_at DESC LIMIT ?"
	args = append(args, limit)

	rows, err := db.Query(query, args...)
	if err != nil {
		// Table may not exist on older databases — return empty result
		write(map[string]interface{}{
			"type": "usage_complete",
		})
		return
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var (
			id, durationMs, inputTokens, outputTokens      int
			cacheReadInputTokens, cacheCreationInputTokens int
			gf, chatJid, completedAt                       string
		)
		if err := rows.Scan(&id, &gf, &chatJid, &completedAt, &durationMs,
			&inputTokens, &outputTokens, &cacheReadInputTokens, &cacheCreationInputTokens); err != nil {
			writeError("DB_ERROR", err.Error())
			return
		}
		write(map[string]interface{}{
			"type":                        "usage_row",
			"id":                          id,
			"group_folder":                gf,
			"chat_jid":                    chatJid,
			"completed_at":                completedAt,
			"duration_ms":                 durationMs,
			"input_tokens":                inputTokens,
			"output_tokens":               outputTokens,
			"cache_read_input_tokens":     cacheReadInputTokens,
			"cache_creation_input_tokens": cacheCreationInputTokens,
		})
	}

	write(map[string]interface{}{
		"type": "usage_complete",
	})
}
