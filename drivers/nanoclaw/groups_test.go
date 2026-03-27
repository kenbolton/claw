// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

func TestHandleGroups(t *testing.T) {
	dir := testSourceDir(t)
	createTestDB(t, dir)
	addTestGroup(t, dir, "jid-main", "main", "main", true)
	addTestGroup(t, dir, "jid-dev", "dev", "dev", false)

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	handleGroups(map[string]interface{}{
		"type":       "groups_request",
		"source_dir": dir,
	})

	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))

	if len(lines) < 3 {
		t.Fatalf("expected at least 3 lines (2 groups + complete), got %d: %s", len(lines), buf.String())
	}

	// Check group messages.
	var groups []map[string]interface{}
	for _, line := range lines {
		var msg map[string]interface{}
		if err := json.Unmarshal(line, &msg); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		if msg["type"] == "group" {
			groups = append(groups, msg)
		}
		if msg["type"] == "groups_complete" {
			break
		}
	}

	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}

	// Groups are ordered by name (dev, main).
	if groups[0]["name"] != "dev" {
		t.Errorf("expected first group 'dev', got %v", groups[0]["name"])
	}
	if groups[1]["name"] != "main" {
		t.Errorf("expected second group 'main', got %v", groups[1]["name"])
	}
	if groups[1]["is_main"] != true {
		t.Errorf("expected main group is_main=true")
	}
}

func TestHandleGroupsEmpty(t *testing.T) {
	dir := testSourceDir(t)
	createTestDB(t, dir)

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	handleGroups(map[string]interface{}{
		"type":       "groups_request",
		"source_dir": dir,
	})

	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))

	if len(lines) != 1 {
		t.Fatalf("expected 1 line (groups_complete only), got %d", len(lines))
	}

	var msg map[string]interface{}
	if err := json.Unmarshal(lines[0], &msg); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if msg["type"] != "groups_complete" {
		t.Errorf("expected groups_complete, got %v", msg["type"])
	}
}

func TestHandleGroupsNoDB(t *testing.T) {
	dir := t.TempDir() // no store/messages.db

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	handleGroups(map[string]interface{}{
		"type":       "groups_request",
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
