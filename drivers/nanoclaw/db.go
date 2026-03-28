// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// openDB opens the NanoClaw messages.db at sourceDir/store/messages.db (read-only).
func openDB(sourceDir string) (*sql.DB, error) {
	dbPath := filepath.Join(sourceDir, "store", "messages.db")
	if _, err := os.Stat(dbPath); err != nil {
		return nil, fmt.Errorf("messages.db not found at %s", dbPath)
	}
	return sql.Open("sqlite", dbPath+"?mode=ro")
}

// detectArchVersion reads the NanoClaw version from package.json in sourceDir.
func detectArchVersion(sourceDir string) string {
	if sourceDir == "" {
		sourceDir = findSourceDir()
	}
	data, err := os.ReadFile(filepath.Join(sourceDir, "package.json"))
	if err != nil {
		return "unknown"
	}
	var pkg struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(data, &pkg) == nil && pkg.Version != "" {
		return pkg.Version
	}
	return "unknown"
}

// probeNanoClaw returns confidence (0.0-1.0) that sourceDir is a NanoClaw install.
func probeNanoClaw(sourceDir string) float64 {
	if sourceDir == "" {
		return 0
	}
	checks := []struct {
		path   string
		weight float64
	}{
		{filepath.Join(sourceDir, "store", "messages.db"), 0.6},
		{filepath.Join(sourceDir, "groups"), 0.2},
		{filepath.Join(sourceDir, "package.json"), 0.1},
		{filepath.Join(sourceDir, "data", "sessions"), 0.1},
	}
	var score float64
	for _, c := range checks {
		if _, err := os.Stat(c.path); err == nil {
			score += c.weight
		}
	}
	return score
}

// findSourceDir locates the NanoClaw installation directory.
// Resolution: NANOCLAW_DIR env → walk up from binary → ~/src/nanoclaw.
func findSourceDir() string {
	if env := os.Getenv("NANOCLAW_DIR"); env != "" {
		return env
	}
	exe, err := os.Executable()
	if err == nil {
		dir := filepath.Dir(exe)
		for _, parent := range []string{dir, filepath.Dir(dir)} {
			if _, err := os.Stat(filepath.Join(parent, "store", "messages.db")); err == nil {
				return parent
			}
			if _, err := os.Stat(filepath.Join(parent, ".env")); err == nil {
				return parent
			}
		}
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "src", "nanoclaw")
}

// GroupRow represents a row from registered_groups.
type GroupRow struct {
	JID             string
	Name            string
	Folder          string
	TriggerPattern  string
	RequiresTrigger bool
	IsMain          bool
	ContainerConfig *string
}

// readGroupRows reads all registered groups from the DB.
func readGroupRows(sourceDir string) ([]GroupRow, error) {
	db, err := openDB(sourceDir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	rows, err := db.Query(`
		SELECT jid, name, folder, trigger_pattern,
		       requires_trigger, is_main, container_config
		FROM registered_groups
		ORDER BY name
	`)
	if err != nil {
		return nil, fmt.Errorf("query failed: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var groups []GroupRow
	for rows.Next() {
		var g GroupRow
		if err := rows.Scan(
			&g.JID, &g.Name, &g.Folder, &g.TriggerPattern,
			&g.RequiresTrigger, &g.IsMain, &g.ContainerConfig,
		); err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

// findGroup fuzzy-matches a group by name or folder.
func findGroup(groups []GroupRow, query string) (*GroupRow, error) {
	q := strings.ToLower(query)
	// Exact match
	for i := range groups {
		if strings.ToLower(groups[i].Name) == q || strings.ToLower(groups[i].Folder) == q {
			return &groups[i], nil
		}
	}
	// Partial match
	var matches []*GroupRow
	for i := range groups {
		if strings.Contains(strings.ToLower(groups[i].Name), q) ||
			strings.Contains(strings.ToLower(groups[i].Folder), q) {
			matches = append(matches, &groups[i])
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		names := make([]string, len(matches))
		for i, m := range matches {
			names[i] = fmt.Sprintf("%q", m.Name)
		}
		return nil, fmt.Errorf("ambiguous group %q: matches %s", query, strings.Join(names, ", "))
	}
	return nil, fmt.Errorf("no group matching %q", query)
}

// MessageRow represents a row from the messages table.
type MessageRow struct {
	SenderName   string
	Content      string
	Timestamp    string
	IsFromMe     bool
	IsBotMessage bool
}

// readMessages reads messages for a chat JID, ordered by timestamp.
// If limit > 0, returns the last N messages.
func readMessages(sourceDir, chatJID string, limit int) ([]MessageRow, error) {
	db, err := openDB(sourceDir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	query := `
		SELECT sender_name, content, timestamp, is_from_me, is_bot_message
		FROM messages
		WHERE chat_jid = ?
		ORDER BY timestamp DESC
	`
	args := []interface{}{chatJID}
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("messages query failed: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var msgs []MessageRow
	for rows.Next() {
		var m MessageRow
		if err := rows.Scan(&m.SenderName, &m.Content, &m.Timestamp, &m.IsFromMe, &m.IsBotMessage); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Reverse to chronological order
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs, nil
}

// readNewMessages reads messages newer than a given timestamp.
func readNewMessages(sourceDir, chatJID, afterTimestamp string) ([]MessageRow, error) {
	db, err := openDB(sourceDir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	rows, err := db.Query(`
		SELECT sender_name, content, timestamp, is_from_me, is_bot_message
		FROM messages
		WHERE chat_jid = ? AND timestamp > ?
		ORDER BY timestamp ASC
	`, chatJID, afterTimestamp)
	if err != nil {
		return nil, fmt.Errorf("messages query failed: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var msgs []MessageRow
	for rows.Next() {
		var m MessageRow
		if err := rows.Scan(&m.SenderName, &m.Content, &m.Timestamp, &m.IsFromMe, &m.IsBotMessage); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

// readSecrets reads secret key-value pairs from .env.
func readSecrets(sourceDir string) map[string]string {
	secretKeys := []string{
		"CLAUDE_CODE_OAUTH_TOKEN",
		"ANTHROPIC_API_KEY",
		"ANTHROPIC_BASE_URL",
		"ANTHROPIC_AUTH_TOKEN",
		"OLLAMA_HOST",
	}

	secrets := map[string]string{}
	envPath := filepath.Join(sourceDir, ".env")
	data, err := os.ReadFile(envPath)
	if err != nil {
		return secrets
	}

	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if idx := strings.Index(line, "="); idx > 0 {
			key := strings.TrimSpace(line[:idx])
			val := strings.TrimSpace(line[idx+1:])
			for _, sk := range secretKeys {
				if key == sk {
					secrets[key] = val
					break
				}
			}
		}
	}
	return secrets
}

// resolveGroup resolves a group from the request fields (group name or JID).
// Returns the group row and the resolved source dir.
func resolveGroup(sourceDir, groupName, jid string) (*GroupRow, string, error) {
	if sourceDir == "" {
		sourceDir = findSourceDir()
	}

	groups, err := readGroupRows(sourceDir)
	if err != nil {
		return nil, sourceDir, fmt.Errorf("failed to read groups: %w", err)
	}

	if groupName != "" {
		g, err := findGroup(groups, groupName)
		if err != nil {
			return nil, sourceDir, err
		}
		return g, sourceDir, nil
	}

	if jid != "" {
		for i := range groups {
			if groups[i].JID == jid {
				return &groups[i], sourceDir, nil
			}
		}
		return nil, sourceDir, fmt.Errorf("no group with JID %q", jid)
	}

	// Default: main group
	for i := range groups {
		if groups[i].IsMain {
			return &groups[i], sourceDir, nil
		}
	}
	return nil, sourceDir, fmt.Errorf("no group specified and no main group found")
}
