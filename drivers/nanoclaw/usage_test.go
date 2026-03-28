// SPDX-License-Identifier: AGPL-3.0-or-later
package main

// Tests for handleUsage (usage_request NDJSON handler).
//
// Test matrix:
//   - TestHandleUsage           — two rows, verifies DESC ordering and token counts
//   - TestHandleUsageWithFilter — group_folder filter returns only matching rows
//   - TestHandleUsageEmpty      — table exists but has no rows → usage_complete only
//   - TestHandleUsageNoTable    — DB exists but run_usage table absent → graceful empty
//   - TestHandleUsageNoDB       — no messages.db at all → DB_ERROR

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// addTestRunUsage creates the run_usage table in the test DB and optionally
// inserts the given rows. Pass nil for rows to create an empty table.
func addTestRunUsage(t *testing.T, dir string, rows []map[string]interface{}) {
	t.Helper()
	dbPath := filepath.Join(dir, "store", "messages.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS run_usage (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			group_folder TEXT NOT NULL,
			chat_jid TEXT NOT NULL,
			completed_at TEXT NOT NULL,
			duration_ms INTEGER NOT NULL,
			input_tokens INTEGER NOT NULL DEFAULT 0,
			output_tokens INTEGER NOT NULL DEFAULT 0,
			cache_read_input_tokens INTEGER NOT NULL DEFAULT 0,
			cache_creation_input_tokens INTEGER NOT NULL DEFAULT 0
		);
		CREATE INDEX IF NOT EXISTS idx_run_usage_completed ON run_usage(completed_at);
		CREATE INDEX IF NOT EXISTS idx_run_usage_group ON run_usage(group_folder);
	`)
	if err != nil {
		t.Fatal(err)
	}

	for _, row := range rows {
		_, err = db.Exec(`INSERT INTO run_usage
			(group_folder, chat_jid, completed_at, duration_ms,
			 input_tokens, output_tokens, cache_read_input_tokens, cache_creation_input_tokens)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			row["group_folder"], row["chat_jid"], row["completed_at"], row["duration_ms"],
			row["input_tokens"], row["output_tokens"], row["cache_read"], row["cache_write"],
		)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestHandleUsage(t *testing.T) {
	dir := testSourceDir(t)
	createTestDB(t, dir)
	addTestRunUsage(t, dir, []map[string]interface{}{
		{
			"group_folder":  "main",
			"chat_jid":      "jid-main",
			"completed_at":  "2026-03-28T10:00:00Z",
			"duration_ms":   5000,
			"input_tokens":  1000,
			"output_tokens": 500,
			"cache_read":    800,
			"cache_write":   200,
		},
		{
			"group_folder":  "dev",
			"chat_jid":      "jid-dev",
			"completed_at":  "2026-03-28T11:00:00Z",
			"duration_ms":   3000,
			"input_tokens":  2000,
			"output_tokens": 1000,
			"cache_read":    1600,
			"cache_write":   400,
		},
	})

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	handleUsage(map[string]interface{}{
		"type":       "usage_request",
		"source_dir": dir,
	})

	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))

	// Expect 2 usage_row messages + 1 usage_complete
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d: %s", len(lines), buf.String())
	}

	var rows []map[string]interface{}
	for _, line := range lines {
		var msg map[string]interface{}
		if err := json.Unmarshal(line, &msg); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		if msg["type"] == "usage_row" {
			rows = append(rows, msg)
		}
		if msg["type"] == "usage_complete" {
			break
		}
	}

	if len(rows) != 2 {
		t.Fatalf("expected 2 usage rows, got %d", len(rows))
	}

	// Rows are ordered by completed_at DESC, so dev comes first
	if rows[0]["group_folder"] != "dev" {
		t.Errorf("expected first row 'dev', got %v", rows[0]["group_folder"])
	}
	if rows[1]["group_folder"] != "main" {
		t.Errorf("expected second row 'main', got %v", rows[1]["group_folder"])
	}

	// Verify token counts
	if int(rows[0]["input_tokens"].(float64)) != 2000 {
		t.Errorf("expected input_tokens=2000, got %v", rows[0]["input_tokens"])
	}
	if int(rows[0]["cache_read_input_tokens"].(float64)) != 1600 {
		t.Errorf("expected cache_read=1600, got %v", rows[0]["cache_read_input_tokens"])
	}
}

func TestHandleUsageWithFilter(t *testing.T) {
	dir := testSourceDir(t)
	createTestDB(t, dir)
	addTestRunUsage(t, dir, []map[string]interface{}{
		{
			"group_folder":  "main",
			"chat_jid":      "jid-main",
			"completed_at":  "2026-03-28T10:00:00Z",
			"duration_ms":   5000,
			"input_tokens":  1000,
			"output_tokens": 500,
			"cache_read":    0,
			"cache_write":   0,
		},
		{
			"group_folder":  "dev",
			"chat_jid":      "jid-dev",
			"completed_at":  "2026-03-28T11:00:00Z",
			"duration_ms":   3000,
			"input_tokens":  2000,
			"output_tokens": 1000,
			"cache_read":    0,
			"cache_write":   0,
		},
	})

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	handleUsage(map[string]interface{}{
		"type":         "usage_request",
		"source_dir":   dir,
		"group_folder": "main",
	})

	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))

	// Expect 1 usage_row + 1 usage_complete
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %s", len(lines), buf.String())
	}

	var msg map[string]interface{}
	if err := json.Unmarshal(lines[0], &msg); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if msg["type"] != "usage_row" {
		t.Errorf("expected usage_row, got %v", msg["type"])
	}
	if msg["group_folder"] != "main" {
		t.Errorf("expected group_folder=main, got %v", msg["group_folder"])
	}
}

func TestHandleUsageEmpty(t *testing.T) {
	dir := testSourceDir(t)
	createTestDB(t, dir)
	addTestRunUsage(t, dir, nil)

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	handleUsage(map[string]interface{}{
		"type":       "usage_request",
		"source_dir": dir,
	})

	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)

	var msg map[string]interface{}
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &msg); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if msg["type"] != "usage_complete" {
		t.Errorf("expected usage_complete, got %v", msg["type"])
	}
}

func TestHandleUsageNoTable(t *testing.T) {
	dir := testSourceDir(t)
	createTestDB(t, dir) // DB exists but no run_usage table

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	handleUsage(map[string]interface{}{
		"type":       "usage_request",
		"source_dir": dir,
	})

	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)

	var msg map[string]interface{}
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &msg); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	// Should gracefully return usage_complete when table doesn't exist
	if msg["type"] != "usage_complete" {
		t.Errorf("expected usage_complete for missing table, got %v", msg["type"])
	}
}

func TestHandleUsageNoDB(t *testing.T) {
	dir := t.TempDir() // no store/messages.db

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	handleUsage(map[string]interface{}{
		"type":       "usage_request",
		"source_dir": dir,
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
	if msg["code"] != "DB_ERROR" {
		t.Errorf("expected DB_ERROR, got %v", msg["code"])
	}
}
