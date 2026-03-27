// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestHandleSessions(t *testing.T) {
	dir := t.TempDir()
	sessDir := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ZEPTOCLAW_DIR", dir)

	// Create test session files.
	sess1 := map[string]interface{}{
		"key":        "sess-1",
		"updated_at": "2026-03-27T10:00:00Z",
		"messages": []map[string]string{
			{"content": "Hello from session 1"},
			{"content": "Second message"},
		},
	}
	sess2 := map[string]interface{}{
		"key":        "sess-2",
		"updated_at": "2026-03-26T09:00:00Z",
		"messages": []map[string]string{
			{"content": "Hello from session 2"},
		},
	}

	for name, s := range map[string]interface{}{"sess-1.json": sess1, "sess-2.json": sess2} {
		data, _ := json.Marshal(s)
		if err := os.WriteFile(filepath.Join(sessDir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	handleSessions(map[string]interface{}{
		"type":       "sessions_request",
		"source_dir": "",
		"group":      "default",
		"limit":      float64(50),
	})

	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))

	var sessions []map[string]interface{}
	var complete bool
	for _, line := range lines {
		var msg map[string]interface{}
		if err := json.Unmarshal(line, &msg); err != nil {
			t.Fatalf("invalid JSON line: %v\nline: %s", err, line)
		}
		switch msg["type"] {
		case "session":
			sessions = append(sessions, msg)
		case "sessions_complete":
			complete = true
		}
	}

	if !complete {
		t.Fatal("missing sessions_complete")
	}

	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}

	// Most recent first.
	if sessions[0]["session_id"] != "sess-1" {
		t.Errorf("expected first session sess-1, got %v", sessions[0]["session_id"])
	}
	if sessions[1]["session_id"] != "sess-2" {
		t.Errorf("expected second session sess-2, got %v", sessions[1]["session_id"])
	}
	if v, ok := sessions[0]["message_count"].(float64); !ok || int(v) != 2 {
		t.Errorf("expected 2 messages in sess-1, got %v", sessions[0]["message_count"])
	}
}

func TestHandleSessionsEmpty(t *testing.T) {
	dir := t.TempDir()
	sessDir := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ZEPTOCLAW_DIR", dir)

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	handleSessions(map[string]interface{}{
		"type":       "sessions_request",
		"source_dir": "",
		"group":      "default",
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

func TestHandleSessionsLimit(t *testing.T) {
	dir := t.TempDir()
	sessDir := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ZEPTOCLAW_DIR", dir)

	// Create 3 session files.
	for i := 1; i <= 3; i++ {
		sess := map[string]interface{}{
			"key":        "sess-" + string(rune('0'+i)),
			"updated_at": "2026-03-2" + string(rune('0'+i+4)) + "T10:00:00Z",
			"messages":   []map[string]string{{"content": "msg"}},
		}
		data, _ := json.Marshal(sess)
		name := "sess-" + string(rune('0'+i)) + ".json"
		if err := os.WriteFile(filepath.Join(sessDir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	handleSessions(map[string]interface{}{
		"type":       "sessions_request",
		"source_dir": "",
		"group":      "default",
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
