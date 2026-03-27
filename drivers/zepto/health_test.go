// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func makeJWT(t *testing.T, exp int64) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d}`, exp)))
	return header + "." + payload + ".signature"
}

// --- JWT expiry tests ---

func TestZeptoJWTExpiry(t *testing.T) {
	tests := []struct {
		name    string
		token   string
		wantErr bool
	}{
		{"valid JWT", makeJWT(t, time.Now().Add(30*24*time.Hour).Unix()), false},
		{"not a JWT", "not-a-jwt", true},
		{"invalid base64", "header.!!!invalid!!!.sig", true},
		{"no exp claim", func() string {
			header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256"}`))
			payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"test"}`))
			return header + "." + payload + ".sig"
		}(), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := zeptoJWTExpiry(tt.token)
			if (err != nil) != tt.wantErr {
				t.Errorf("zeptoJWTExpiry() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// --- Credential checks ---

func TestCheckZeptoCredentials(t *testing.T) {
	t.Run("oauth token from env", func(t *testing.T) {
		token := makeJWT(t, time.Now().Add(60*24*time.Hour).Unix())
		t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", token)

		dataDir := t.TempDir()
		status, detail, _ := checkZeptoCredentials(dataDir)
		if status != "pass" {
			t.Errorf("expected pass, got %s: %s", status, detail)
		}
	})

	t.Run("oauth token expiring soon", func(t *testing.T) {
		token := makeJWT(t, time.Now().Add(3*24*time.Hour).Unix())
		t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", token)

		dataDir := t.TempDir()
		status, _, _ := checkZeptoCredentials(dataDir)
		if status != "warn" {
			t.Errorf("expected warn, got %s", status)
		}
	})

	t.Run("oauth token expired", func(t *testing.T) {
		token := makeJWT(t, time.Now().Add(-24*time.Hour).Unix())
		t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", token)

		dataDir := t.TempDir()
		status, _, _ := checkZeptoCredentials(dataDir)
		if status != "fail" {
			t.Errorf("expected fail, got %s", status)
		}
	})

	t.Run("api key from env", func(t *testing.T) {
		t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")
		t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test-key")

		dataDir := t.TempDir()
		status, detail, _ := checkZeptoCredentials(dataDir)
		if status != "pass" {
			t.Errorf("expected pass, got %s: %s", status, detail)
		}
	})

	t.Run("api key from config.json", func(t *testing.T) {
		t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")
		t.Setenv("ANTHROPIC_API_KEY", "")

		dataDir := t.TempDir()
		cfg := map[string]interface{}{"api_key": "sk-test"}
		data, _ := json.Marshal(cfg)
		if err := os.WriteFile(filepath.Join(dataDir, "config.json"), data, 0o644); err != nil {
			t.Fatal(err)
		}

		status, detail, _ := checkZeptoCredentials(dataDir)
		if status != "pass" {
			t.Errorf("expected pass, got %s: %s", status, detail)
		}
	})

	t.Run("no credentials", func(t *testing.T) {
		t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")
		t.Setenv("ANTHROPIC_API_KEY", "")

		dataDir := t.TempDir()
		status, _, _ := checkZeptoCredentials(dataDir)
		if status != "fail" {
			t.Errorf("expected fail, got %s", status)
		}
	})
}

// --- Disk checks ---

func TestCheckZeptoDisk(t *testing.T) {
	t.Run("valid directory", func(t *testing.T) {
		dir := t.TempDir()
		status, _, _ := checkZeptoDisk(dir)
		if status != "pass" && status != "warn" {
			t.Errorf("expected pass or warn, got %s", status)
		}
	})

	t.Run("nonexistent directory", func(t *testing.T) {
		status, _, _ := checkZeptoDisk("/nonexistent/path/that/does/not/exist")
		if status != "fail" {
			t.Errorf("expected fail, got %s", status)
		}
	})
}

func TestZeptoDiskUsagePercent(t *testing.T) {
	dir := t.TempDir()
	pct, err := zeptoDiskUsagePercent(dir)
	if err != nil {
		t.Fatalf("zeptoDiskUsagePercent failed: %v", err)
	}
	if pct < 0 || pct > 100 {
		t.Errorf("expected 0-100, got %d", pct)
	}
}

// --- Sessions checks ---

func TestCheckZeptoSessions(t *testing.T) {
	t.Run("no sessions directory", func(t *testing.T) {
		dataDir := t.TempDir()
		status, detail, _ := checkZeptoSessions(dataDir)
		if status != "pass" {
			t.Errorf("expected pass, got %s: %s", status, detail)
		}
	})

	t.Run("empty sessions directory", func(t *testing.T) {
		dataDir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dataDir, "sessions"), 0o755); err != nil {
			t.Fatal(err)
		}

		status, _, _ := checkZeptoSessions(dataDir)
		if status != "pass" {
			t.Errorf("expected pass, got %s", status)
		}
	})

	t.Run("stale session files", func(t *testing.T) {
		dataDir := t.TempDir()
		sessDir := filepath.Join(dataDir, "sessions")
		if err := os.MkdirAll(sessDir, 0o755); err != nil {
			t.Fatal(err)
		}

		// Create a stale session file.
		f := filepath.Join(sessDir, "old-session.json")
		if err := os.WriteFile(f, []byte(`{"session":"data"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		// Set mod time to 20 minutes ago.
		staleTime := time.Now().Add(-20 * time.Minute)
		if err := os.Chtimes(f, staleTime, staleTime); err != nil {
			t.Fatal(err)
		}

		status, _, _ := checkZeptoSessions(dataDir)
		if status != "warn" {
			t.Errorf("expected warn, got %s", status)
		}
	})
}

// --- handleHealth integration test ---

func TestHandleHealthOutput(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("ZEPTOCLAW_DIR", dataDir)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test-key")

	msg := map[string]interface{}{
		"type":       "health_request",
		"source_dir": "",
		"group":      "",
		"checks":     []interface{}{"credentials", "disk"},
	}

	// Capture stdout.
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	handleHealth(msg)

	_ = w.Close()
	os.Stdout = oldStdout

	var buf [8192]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])

	// Parse NDJSON lines.
	var lines []string
	for _, line := range splitLines(output) {
		line = trimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}

	if len(lines) < 3 { // 2 check_results + 1 health_complete
		t.Fatalf("expected at least 3 NDJSON lines, got %d:\n%s", len(lines), output)
	}

	// Verify we got check_result messages.
	checkNames := map[string]bool{}
	for _, line := range lines {
		var msg map[string]interface{}
		if json.Unmarshal([]byte(line), &msg) != nil {
			continue
		}
		if msg["type"] == "check_result" {
			name, _ := msg["name"].(string)
			checkNames[name] = true
		}
	}

	for _, expected := range []string{"credentials", "disk"} {
		if !checkNames[expected] {
			t.Errorf("missing check_result for %s", expected)
		}
	}
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func trimSpace(s string) string {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\r' || s[i] == '\n') {
		i++
	}
	j := len(s)
	for j > i && (s[j-1] == ' ' || s[j-1] == '\t' || s[j-1] == '\r' || s[j-1] == '\n') {
		j--
	}
	return s[i:j]
}
