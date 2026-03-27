// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReadRecentSessions(t *testing.T) {
	t.Run("empty directory", func(t *testing.T) {
		dir := t.TempDir()
		entries, err := readRecentSessions(dir, 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(entries) != 0 {
			t.Errorf("expected 0 entries, got %d", len(entries))
		}
	})

	t.Run("nonexistent directory", func(t *testing.T) {
		entries, err := readRecentSessions("/nonexistent/sessions", 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(entries) != 0 {
			t.Errorf("expected 0 entries, got %d", len(entries))
		}
	})

	t.Run("single CLI session", func(t *testing.T) {
		dir := t.TempDir()
		sess := sessionFile{
			Key:       "cli:session-1",
			UpdatedAt: "2026-03-27T10:00:00Z",
			Messages: []sessionMessage{
				{Role: "user", Content: "hello"},
				{Role: "assistant", Content: "hi there"},
			},
		}
		writeSessionFile(t, dir, "s1.json", sess)

		entries, err := readRecentSessions(dir, 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(entries) != 2 {
			t.Fatalf("expected 2 entries, got %d", len(entries))
		}

		if entries[0].sender != "You" {
			t.Errorf("expected sender=You, got %q", entries[0].sender)
		}
		if entries[0].content != "hello" {
			t.Errorf("expected content=hello, got %q", entries[0].content)
		}
		if entries[0].isBot {
			t.Error("expected isBot=false for user")
		}

		if entries[1].sender != "Agent" {
			t.Errorf("expected sender=Agent, got %q", entries[1].sender)
		}
		if !entries[1].isBot {
			t.Error("expected isBot=true for assistant")
		}
	})

	t.Run("filters out non-CLI sessions", func(t *testing.T) {
		dir := t.TempDir()
		writeSessionFile(t, dir, "api.json", sessionFile{
			Key:       "api:session-1",
			UpdatedAt: "2026-03-27T10:00:00Z",
			Messages:  []sessionMessage{{Role: "user", Content: "api request"}},
		})
		writeSessionFile(t, dir, "cli.json", sessionFile{
			Key:       "cli:session-2",
			UpdatedAt: "2026-03-27T11:00:00Z",
			Messages:  []sessionMessage{{Role: "user", Content: "cli request"}},
		})

		entries, err := readRecentSessions(dir, 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(entries) != 1 {
			t.Fatalf("expected 1 entry (only CLI), got %d", len(entries))
		}
		if entries[0].content != "cli request" {
			t.Errorf("expected 'cli request', got %q", entries[0].content)
		}
	})

	t.Run("filters out system and tool roles", func(t *testing.T) {
		dir := t.TempDir()
		writeSessionFile(t, dir, "s.json", sessionFile{
			Key:       "cli:session-1",
			UpdatedAt: "2026-03-27T10:00:00Z",
			Messages: []sessionMessage{
				{Role: "system", Content: "system prompt"},
				{Role: "user", Content: "hello"},
				{Role: "tool", Content: "tool result"},
				{Role: "assistant", Content: "response"},
			},
		})

		entries, err := readRecentSessions(dir, 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(entries) != 2 {
			t.Fatalf("expected 2 entries (user+assistant), got %d", len(entries))
		}
	})

	t.Run("respects limit", func(t *testing.T) {
		dir := t.TempDir()
		writeSessionFile(t, dir, "s.json", sessionFile{
			Key:       "cli:session-1",
			UpdatedAt: "2026-03-27T10:00:00Z",
			Messages: []sessionMessage{
				{Role: "user", Content: "msg1"},
				{Role: "assistant", Content: "msg2"},
				{Role: "user", Content: "msg3"},
				{Role: "assistant", Content: "msg4"},
				{Role: "user", Content: "msg5"},
			},
		})

		entries, err := readRecentSessions(dir, 3)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(entries) != 3 {
			t.Fatalf("expected 3 entries, got %d", len(entries))
		}
		// Should return the last 3 entries
		if entries[0].content != "msg3" {
			t.Errorf("expected msg3, got %q", entries[0].content)
		}
	})

	t.Run("skips malformed JSON files", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "bad.json"), []byte("not json"), 0o644); err != nil {
			t.Fatal(err)
		}
		writeSessionFile(t, dir, "good.json", sessionFile{
			Key:       "cli:session-1",
			UpdatedAt: "2026-03-27T10:00:00Z",
			Messages:  []sessionMessage{{Role: "user", Content: "hello"}},
		})

		entries, err := readRecentSessions(dir, 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(entries) != 1 {
			t.Fatalf("expected 1 entry, got %d", len(entries))
		}
	})

	t.Run("uses file mod time when UpdatedAt is empty", func(t *testing.T) {
		dir := t.TempDir()
		writeSessionFile(t, dir, "s.json", sessionFile{
			Key:       "cli:session-1",
			UpdatedAt: "",
			Messages:  []sessionMessage{{Role: "user", Content: "hello"}},
		})

		entries, err := readRecentSessions(dir, 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(entries) != 1 {
			t.Fatalf("expected 1 entry, got %d", len(entries))
		}
		// Timestamp should be set from file mod time
		if entries[0].timestamp == "" {
			t.Error("expected non-empty timestamp from file mod time")
		}
	})
}

func TestReadSessionsSince(t *testing.T) {
	t.Run("returns entries newer than timestamp", func(t *testing.T) {
		dir := t.TempDir()

		// Write a session file
		writeSessionFile(t, dir, "s.json", sessionFile{
			Key:       "cli:session-1",
			UpdatedAt: "2026-03-27T12:00:00Z",
			Messages: []sessionMessage{
				{Role: "user", Content: "new message"},
			},
		})

		// Set mod time to the future so it's "newer" than afterTS
		future := time.Now().Add(1 * time.Hour)
		if err := os.Chtimes(filepath.Join(dir, "s.json"), future, future); err != nil {
			t.Fatal(err)
		}

		afterTS := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
		entries, err := readSessionsSince(dir, afterTS)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(entries) != 1 {
			t.Fatalf("expected 1 entry, got %d", len(entries))
		}
		if entries[0].content != "new message" {
			t.Errorf("expected 'new message', got %q", entries[0].content)
		}
	})

	t.Run("filters out old files", func(t *testing.T) {
		dir := t.TempDir()
		writeSessionFile(t, dir, "old.json", sessionFile{
			Key:      "cli:session-old",
			Messages: []sessionMessage{{Role: "user", Content: "old"}},
		})
		// Set mod time to the past
		past := time.Now().Add(-2 * time.Hour)
		if err := os.Chtimes(filepath.Join(dir, "old.json"), past, past); err != nil {
			t.Fatal(err)
		}

		afterTS := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
		entries, err := readSessionsSince(dir, afterTS)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(entries) != 0 {
			t.Errorf("expected 0 entries for old file, got %d", len(entries))
		}
	})

	t.Run("invalid timestamp returns error", func(t *testing.T) {
		dir := t.TempDir()
		_, err := readSessionsSince(dir, "not-a-timestamp")
		if err == nil {
			t.Error("expected error for invalid timestamp")
		}
	})

	t.Run("nonexistent directory returns nil", func(t *testing.T) {
		entries, err := readSessionsSince("/nonexistent/sessions", time.Now().UTC().Format(time.RFC3339))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(entries) != 0 {
			t.Errorf("expected 0 entries, got %d", len(entries))
		}
	})

	t.Run("filters out non-CLI sessions", func(t *testing.T) {
		dir := t.TempDir()
		writeSessionFile(t, dir, "api.json", sessionFile{
			Key:      "api:session-1",
			Messages: []sessionMessage{{Role: "user", Content: "api request"}},
		})
		future := time.Now().Add(1 * time.Hour)
		if err := os.Chtimes(filepath.Join(dir, "api.json"), future, future); err != nil {
			t.Fatal(err)
		}

		afterTS := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
		entries, err := readSessionsSince(dir, afterTS)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(entries) != 0 {
			t.Errorf("expected 0 entries for non-CLI, got %d", len(entries))
		}
	})

	t.Run("entries sorted by timestamp", func(t *testing.T) {
		dir := t.TempDir()

		// Two session files with different mod times
		writeSessionFile(t, dir, "b.json", sessionFile{
			Key:      "cli:session-b",
			Messages: []sessionMessage{{Role: "user", Content: "second"}},
		})
		writeSessionFile(t, dir, "a.json", sessionFile{
			Key:      "cli:session-a",
			Messages: []sessionMessage{{Role: "user", Content: "first"}},
		})

		// Set mod times: a is older, b is newer
		t1 := time.Now().Add(1 * time.Hour)
		t2 := time.Now().Add(2 * time.Hour)
		if err := os.Chtimes(filepath.Join(dir, "a.json"), t1, t1); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(filepath.Join(dir, "b.json"), t2, t2); err != nil {
			t.Fatal(err)
		}

		afterTS := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
		entries, err := readSessionsSince(dir, afterTS)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(entries) < 2 {
			t.Fatalf("expected 2 entries, got %d", len(entries))
		}
		// Should be sorted by timestamp (ascending)
		if entries[0].timestamp > entries[1].timestamp {
			t.Errorf("expected entries sorted by timestamp, got %s > %s",
				entries[0].timestamp, entries[1].timestamp)
		}
	})
}

// writeSessionFile writes a session file to the given directory.
func writeSessionFile(t *testing.T, dir, name string, sess sessionFile) {
	t.Helper()
	data, err := json.Marshal(sess)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		t.Fatal(err)
	}
}
