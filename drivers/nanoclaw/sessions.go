// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// handleSessions lists recent sessions for a group.
func handleSessions(msg map[string]interface{}) {
	sourceDir, _ := msg["source_dir"].(string)
	if sourceDir == "" {
		sourceDir = findSourceDir()
	}

	groupName, _ := msg["group"].(string)
	limit := 50
	if v, ok := msg["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}

	group, sourceDir, err := resolveGroup(sourceDir, groupName, "")
	if err != nil {
		writeError("GROUP_NOT_FOUND", err.Error())
		return
	}

	db, err := openDB(sourceDir)
	if err != nil {
		writeError("DB_ERROR", err.Error())
		return
	}
	defer func() { _ = db.Close() }()

	// Query distinct sessions by grouping messages by session_id (conversation boundaries).
	// NanoClaw messages table may not have an explicit session_id column, so we identify
	// sessions by conversation gaps (>30 min between messages) or by date boundaries.
	sessions, err := querySessions(db, group.JID, group.Name, limit)
	if err != nil {
		writeError("DB_ERROR", err.Error())
		return
	}

	for _, s := range sessions {
		write(s)
	}

	// Also emit Claude session UUIDs from JSONL files
	claudeSessions := findClaudeSessions(sourceDir, group.Folder, limit)
	for _, s := range claudeSessions {
		write(s)
	}

	write(map[string]interface{}{
		"type": "sessions_complete",
	})
}

// querySessions derives sessions from the messages table by grouping messages
// by date (one session per day).
func querySessions(db *sql.DB, chatJID, groupName string, limit int) ([]map[string]interface{}, error) {
	rows, err := db.Query(`
		SELECT sender_name, content, timestamp
		FROM messages
		WHERE chat_jid = ?
		ORDER BY timestamp ASC
	`, chatJID)
	if err != nil {
		return nil, fmt.Errorf("sessions query failed: %w", err)
	}
	defer func() { _ = rows.Close() }()

	type msg struct {
		sender    string
		content   string
		timestamp string
	}

	var allMsgs []msg
	for rows.Next() {
		var m msg
		if err := rows.Scan(&m.sender, &m.content, &m.timestamp); err != nil {
			return nil, err
		}
		allMsgs = append(allMsgs, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(allMsgs) == 0 {
		return nil, nil
	}

	// Group messages into sessions by date (one session per day).
	type session struct {
		startedAt    string
		lastActive   string
		messageCount int
		firstContent string
	}

	var sessions []session
	var cur *session

	for _, m := range allMsgs {
		day := ""
		if len(m.timestamp) >= 10 {
			day = m.timestamp[:10]
		}

		prevDay := ""
		if cur != nil && len(cur.startedAt) >= 10 {
			prevDay = cur.startedAt[:10]
		}

		if cur == nil || day != prevDay {
			if cur != nil {
				sessions = append(sessions, *cur)
			}
			cur = &session{
				startedAt:    m.timestamp,
				lastActive:   m.timestamp,
				messageCount: 1,
				firstContent: m.content,
			}
		} else {
			cur.lastActive = m.timestamp
			cur.messageCount++
		}
	}
	if cur != nil {
		sessions = append(sessions, *cur)
	}

	// Reverse to most-recent-first and apply limit.
	for i, j := 0, len(sessions)-1; i < j; i, j = i+1, j-1 {
		sessions[i], sessions[j] = sessions[j], sessions[i]
	}
	if limit > 0 && len(sessions) > limit {
		sessions = sessions[:limit]
	}

	var result []map[string]interface{}
	for _, s := range sessions {
		summary := s.firstContent
		if len(summary) > 100 {
			summary = summary[:97] + "..."
		}
		// Use group+date as a stable session ID (sessions are grouped by day).
		day := s.startedAt
		if len(day) >= 10 {
			day = day[:10]
		}
		result = append(result, map[string]interface{}{
			"type":          "session",
			"session_id":    fmt.Sprintf("%s-%s", groupName, day),
			"group":         groupName,
			"started_at":    s.startedAt,
			"last_active":   s.lastActive,
			"message_count": s.messageCount,
			"summary":       summary,
		})
	}
	return result, nil
}

// findClaudeSessions reads JSONL session files and returns session metadata
// with real Claude session UUIDs that can be used to resume.
func findClaudeSessions(sourceDir, folder string, limit int) []map[string]interface{} {
	projectsDir := filepath.Join(sourceDir, "data", "sessions", folder, ".claude", "projects")

	type sessionFile struct {
		uuid    string
		path    string
		modTime int64
		size    int64
	}

	var files []sessionFile
	_ = filepath.Walk(projectsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		name := strings.TrimSuffix(info.Name(), ".jsonl")
		files = append(files, sessionFile{
			uuid:    name,
			path:    path,
			modTime: info.ModTime().Unix(),
			size:    info.Size(),
		})
		return nil
	})

	// Sort by modification time, newest first
	sort.Slice(files, func(i, j int) bool {
		return files[i].modTime > files[j].modTime
	})

	if limit > 0 && len(files) > limit {
		files = files[:limit]
	}

	var result []map[string]interface{}
	for _, f := range files {
		result = append(result, map[string]interface{}{
			"type":          "session",
			"session_id":    f.uuid,
			"group":         folder,
			"started_at":    "",
			"last_active":   fmt.Sprintf("%d", f.modTime),
			"message_count": 0,
			"summary":       fmt.Sprintf("Claude session %s (%d KB)", f.uuid[:8], f.size/1024),
			"resumable":     true,
		})
	}
	return result
}
