// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func handleWatch(msg map[string]interface{}) {
	lines := 20
	if v, ok := msg["lines"].(float64); ok && v > 0 {
		lines = int(v)
	}

	dataDir := findDataDir()
	sourceDir, _ := msg["source_dir"].(string)
	if sourceDir != "" {
		if _, err := os.Stat(filepath.Join(sourceDir, "config.json")); err == nil {
			dataDir = sourceDir
		}
	}

	sessionsDir := filepath.Join(dataDir, "sessions")
	entries, err := readRecentSessions(sessionsDir, lines)
	if err != nil {
		writeError("WATCH_ERROR", fmt.Sprintf("failed to read sessions: %v", err))
		return
	}

	for _, e := range entries {
		write(map[string]interface{}{
			"type":      "message",
			"timestamp": e.timestamp,
			"sender":    e.sender,
			"content":   e.content,
			"is_bot":    e.isBot,
		})
	}

	// Watch for stdin close in a goroutine, then poll for new session entries
	done := make(chan struct{})
	go func() {
		buf := make([]byte, 1)
		_, _ = os.Stdin.Read(buf)
		close(done)
	}()

	// Track the latest timestamp seen
	lastTS := ""
	if len(entries) > 0 {
		lastTS = entries[len(entries)-1].timestamp
	} else {
		lastTS = time.Now().UTC().Format(time.RFC3339)
	}

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			newEntries, err := readSessionsSince(sessionsDir, lastTS)
			if err != nil {
				continue
			}
			for _, e := range newEntries {
				write(map[string]interface{}{
					"type":      "message",
					"timestamp": e.timestamp,
					"sender":    e.sender,
					"content":   e.content,
					"is_bot":    e.isBot,
				})
				lastTS = e.timestamp
			}
		}
	}
}

type sessionEntry struct {
	timestamp string
	sender    string
	content   string
	isBot     bool
}

// sessionMessage matches the JSON structure inside ZeptoClaw session files.
type sessionMessage struct {
	Role    string `json:"role"` // "user" | "assistant" | "system" | "tool"
	Content string `json:"content"`
}

type sessionFile struct {
	Key       string           `json:"key"`
	UpdatedAt string           `json:"updated_at"`
	Messages  []sessionMessage `json:"messages"`
}

// readRecentSessions reads the N most recent CLI session messages from ~/.zeptoclaw/sessions/.
func readRecentSessions(sessionsDir string, limit int) ([]sessionEntry, error) {
	files, err := filepath.Glob(filepath.Join(sessionsDir, "*.json"))
	if err != nil || len(files) == 0 {
		return nil, nil
	}

	// Sort by modification time, newest first
	type fileTime struct {
		path    string
		modTime time.Time
	}
	var ft []fileTime
	for _, f := range files {
		info, err := os.Stat(f)
		if err != nil {
			continue
		}
		ft = append(ft, fileTime{f, info.ModTime()})
	}
	sort.Slice(ft, func(i, j int) bool {
		return ft[i].modTime.After(ft[j].modTime)
	})

	// Collect entries from most recent sessions until we have enough
	var entries []sessionEntry
	for _, f := range ft {
		data, err := os.ReadFile(f.path)
		if err != nil {
			continue
		}
		var sess sessionFile
		if err := json.Unmarshal(data, &sess); err != nil {
			continue
		}

		// Only include CLI sessions
		if !strings.HasPrefix(sess.Key, "cli:") {
			continue
		}

		ts := sess.UpdatedAt
		if ts == "" {
			info, _ := os.Stat(f.path)
			ts = info.ModTime().UTC().Format(time.RFC3339)
		}

		for _, m := range sess.Messages {
			if m.Role == "system" || m.Role == "tool" {
				continue
			}
			isBot := m.Role == "assistant"
			sender := "You"
			if isBot {
				sender = "Agent"
			}
			entries = append(entries, sessionEntry{
				timestamp: ts,
				sender:    sender,
				content:   m.Content,
				isBot:     isBot,
			})
		}

		if len(entries) >= limit {
			break
		}
	}

	// Return only the last N, in order
	if len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}
	return entries, nil
}

// readSessionsSince returns session entries newer than afterTS.
func readSessionsSince(sessionsDir, afterTS string) ([]sessionEntry, error) {
	after, err := time.Parse(time.RFC3339, afterTS)
	if err != nil {
		return nil, fmt.Errorf("invalid timestamp: %v", err)
	}

	files, err := filepath.Glob(filepath.Join(sessionsDir, "*.json"))
	if err != nil {
		return nil, nil
	}

	var entries []sessionEntry
	for _, f := range files {
		info, err := os.Stat(f)
		if err != nil || !info.ModTime().After(after) {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var sess sessionFile
		if err := json.Unmarshal(data, &sess); err != nil {
			continue
		}
		if !strings.HasPrefix(sess.Key, "cli:") {
			continue
		}
		ts := info.ModTime().UTC().Format(time.RFC3339)
		for _, m := range sess.Messages {
			if m.Role == "system" || m.Role == "tool" {
				continue
			}
			isBot := m.Role == "assistant"
			sender := "You"
			if isBot {
				sender = "Agent"
			}
			entries = append(entries, sessionEntry{
				timestamp: ts,
				sender:    sender,
				content:   m.Content,
				isBot:     isBot,
			})
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].timestamp < entries[j].timestamp
	})
	return entries, nil
}
