// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// addTestMessageAt inserts a message at a specific timestamp.
func addTestMessageAt(t *testing.T, dir, chatJID, timestamp, content string) {
	t.Helper()
	dbPath := filepath.Join(dir, "store", "messages.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	_, err = db.Exec(`INSERT INTO messages (sender_name, content, timestamp, is_from_me, is_bot_message, chat_jid)
		VALUES (?, ?, ?, 0, 0, ?)`, "test", content, timestamp, chatJID)
	if err != nil {
		t.Fatal(err)
	}
}

func TestHandleSessions(t *testing.T) {
	dir := testSourceDir(t)
	createTestDB(t, dir)
	addTestGroup(t, dir, "jid-main", "main", "main", true)

	// Add messages across two days.
	addTestMessageAt(t, dir, "jid-main", "2026-03-26T10:00:00Z", "Hello day 1")
	addTestMessageAt(t, dir, "jid-main", "2026-03-26T10:05:00Z", "More day 1")
	addTestMessageAt(t, dir, "jid-main", "2026-03-27T09:00:00Z", "Hello day 2")

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	handleSessions(map[string]interface{}{
		"type":       "sessions_request",
		"source_dir": dir,
		"group":      "main",
		"limit":      float64(50),
	})

	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))

	// Expect 2 session messages + sessions_complete.
	var sessions []map[string]interface{}
	var complete bool
	for _, line := range lines {
		var msg map[string]interface{}
		if err := json.Unmarshal(line, &msg); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		switch msg["type"] {
		case "session":
			sessions = append(sessions, msg)
		case "sessions_complete":
			complete = true
		case "error":
			t.Fatalf("unexpected error: %v", msg)
		}
	}

	if !complete {
		t.Fatal("missing sessions_complete")
	}

	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}

	// Most recent first.
	if sessions[0]["started_at"] != "2026-03-27T09:00:00Z" {
		t.Errorf("expected first session to be day 2, got %v", sessions[0]["started_at"])
	}
	if sessions[0]["group"] != "main" {
		t.Errorf("expected group 'main', got %v", sessions[0]["group"])
	}
	if v, ok := sessions[0]["message_count"].(float64); !ok || int(v) != 1 {
		t.Errorf("expected 1 message in day 2 session, got %v", sessions[0]["message_count"])
	}
	if v, ok := sessions[1]["message_count"].(float64); !ok || int(v) != 2 {
		t.Errorf("expected 2 messages in day 1 session, got %v", sessions[1]["message_count"])
	}
}

func TestHandleSessionsLimit(t *testing.T) {
	dir := testSourceDir(t)
	createTestDB(t, dir)
	addTestGroup(t, dir, "jid-main", "main", "main", true)

	// Add messages across 3 days.
	for i := 1; i <= 3; i++ {
		addTestMessageAt(t, dir, "jid-main", fmt.Sprintf("2026-03-%02dT10:00:00Z", 25+i), fmt.Sprintf("Day %d", i))
	}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	handleSessions(map[string]interface{}{
		"type":       "sessions_request",
		"source_dir": dir,
		"group":      "main",
		"limit":      float64(2),
	})

	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))

	var sessions int
	for _, line := range lines {
		var msg map[string]interface{}
		_ = json.Unmarshal(line, &msg)
		if msg["type"] == "session" {
			sessions++
		}
	}

	if sessions != 2 {
		t.Errorf("expected 2 sessions with limit=2, got %d", sessions)
	}
}

func TestHandleSessionsGroupNotFound(t *testing.T) {
	dir := testSourceDir(t)
	createTestDB(t, dir)

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	handleSessions(map[string]interface{}{
		"type":       "sessions_request",
		"source_dir": dir,
		"group":      "nonexistent",
		"limit":      float64(50),
	})

	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)

	var msg map[string]interface{}
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &msg); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if msg["type"] != "error" {
		t.Errorf("expected error, got %v", msg["type"])
	}
	if msg["code"] != "GROUP_NOT_FOUND" {
		t.Errorf("expected GROUP_NOT_FOUND, got %v", msg["code"])
	}
}

func TestHandleSessionsEmpty(t *testing.T) {
	dir := testSourceDir(t)
	createTestDB(t, dir)
	addTestGroup(t, dir, "jid-main", "main", "main", true)
	// No messages added.

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	handleSessions(map[string]interface{}{
		"type":       "sessions_request",
		"source_dir": dir,
		"group":      "main",
		"limit":      float64(50),
	})

	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))

	if len(lines) != 1 {
		t.Fatalf("expected 1 line (sessions_complete), got %d", len(lines))
	}
	var msg map[string]interface{}
	_ = json.Unmarshal(lines[0], &msg)
	if msg["type"] != "sessions_complete" {
		t.Errorf("expected sessions_complete, got %v", msg["type"])
	}
}
