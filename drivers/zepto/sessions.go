// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// handleSessions lists recent sessions from ~/.zeptoclaw/sessions/.
func handleSessions(msg map[string]interface{}) {
	limit := 50
	if v, ok := msg["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}
	groupName, _ := msg["group"].(string)

	sessDir := zeptoSessionsDir()
	entries, err := os.ReadDir(sessDir)
	if err != nil {
		writeError("DB_ERROR", fmt.Sprintf("cannot read sessions dir: %v", err))
		return
	}

	type sessionInfo struct {
		Key       string `json:"key"`
		UpdatedAt string `json:"updated_at"`
		MsgCount  int    `json:"message_count"`
		FirstLine string `json:"first_line"`
	}

	var sessions []sessionInfo
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(sessDir, e.Name()))
		if err != nil {
			continue
		}
		var raw struct {
			Key       string `json:"key"`
			UpdatedAt string `json:"updated_at"`
			Messages  []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			continue
		}
		firstLine := ""
		if len(raw.Messages) > 0 {
			firstLine = raw.Messages[0].Content
			if len(firstLine) > 100 {
				firstLine = firstLine[:97] + "..."
			}
		}
		sessions = append(sessions, sessionInfo{
			Key:       raw.Key,
			UpdatedAt: raw.UpdatedAt,
			MsgCount:  len(raw.Messages),
			FirstLine: firstLine,
		})
	}

	// Sort by updated_at descending.
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt > sessions[j].UpdatedAt
	})

	if limit > 0 && len(sessions) > limit {
		sessions = sessions[:limit]
	}

	for _, s := range sessions {
		write(map[string]interface{}{
			"type":          "session",
			"session_id":    s.Key,
			"group":         groupName,
			"started_at":    s.UpdatedAt,
			"last_active":   s.UpdatedAt,
			"message_count": s.MsgCount,
			"summary":       s.FirstLine,
		})
	}

	write(map[string]interface{}{
		"type": "sessions_complete",
	})
}

func zeptoSessionsDir() string {
	home, _ := os.UserHomeDir()
	if env := os.Getenv("ZEPTOCLAW_DIR"); env != "" {
		return filepath.Join(env, "sessions")
	}
	return filepath.Join(home, ".zeptoclaw", "sessions")
}
