// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// testSourceDir creates a temporary NanoClaw-like installation directory.
func testSourceDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// Create directory structure.
	if err := os.MkdirAll(filepath.Join(dir, "store"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "groups"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// createTestDB creates a minimal messages.db with the expected schema.
func createTestDB(t *testing.T, dir string) {
	t.Helper()
	dbPath := filepath.Join(dir, "store", "messages.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS registered_groups (
			jid TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			folder TEXT NOT NULL,
			trigger_pattern TEXT DEFAULT '',
			requires_trigger INTEGER DEFAULT 0,
			is_main INTEGER DEFAULT 0,
			container_config TEXT
		);
		CREATE TABLE IF NOT EXISTS messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			sender_name TEXT,
			content TEXT,
			timestamp TEXT,
			is_from_me INTEGER DEFAULT 0,
			is_bot_message INTEGER DEFAULT 0,
			chat_jid TEXT
		);
	`)
	if err != nil {
		t.Fatal(err)
	}
}

// addTestGroup inserts a group into the test DB.
func addTestGroup(t *testing.T, dir, jid, name, folder string, isMain bool) {
	t.Helper()
	dbPath := filepath.Join(dir, "store", "messages.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	mainInt := 0
	if isMain {
		mainInt = 1
	}
	_, err = db.Exec(`INSERT INTO registered_groups (jid, name, folder, trigger_pattern, requires_trigger, is_main)
		VALUES (?, ?, ?, '', 0, ?)`, jid, name, folder, mainInt)
	if err != nil {
		t.Fatal(err)
	}
}

// addTestMessages inserts N messages into the test DB.
func addTestMessages(t *testing.T, dir, chatJID string, count int) {
	t.Helper()
	dbPath := filepath.Join(dir, "store", "messages.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	for i := 0; i < count; i++ {
		_, err = db.Exec(`INSERT INTO messages (sender_name, content, timestamp, is_from_me, is_bot_message, chat_jid)
			VALUES (?, ?, ?, 0, 0, ?)`, "test", fmt.Sprintf("msg %d", i), time.Now().Format(time.RFC3339), chatJID)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func makeJWT(t *testing.T, exp int64) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d}`, exp)))
	return header + "." + payload + ".signature"
}

func writeEnvFile(t *testing.T, dir string, content string) {
	t.Helper()
	err := os.WriteFile(filepath.Join(dir, ".env"), []byte(content), 0o644)
	if err != nil {
		t.Fatal(err)
	}
}

// --- JWT expiry tests ---

func TestJwtExpiry(t *testing.T) {
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
			_, err := jwtExpiry(tt.token)
			if (err != nil) != tt.wantErr {
				t.Errorf("jwtExpiry() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// --- Credential checks ---

func TestCheckCredentials(t *testing.T) {
	t.Run("oauth token valid", func(t *testing.T) {
		dir := testSourceDir(t)
		token := makeJWT(t, time.Now().Add(60*24*time.Hour).Unix())
		writeEnvFile(t, dir, "CLAUDE_CODE_OAUTH_TOKEN="+token)

		status, detail, _ := checkCredentials(dir)
		if status != "pass" {
			t.Errorf("expected pass, got %s: %s", status, detail)
		}
	})

	t.Run("oauth token expiring soon", func(t *testing.T) {
		dir := testSourceDir(t)
		token := makeJWT(t, time.Now().Add(3*24*time.Hour).Unix())
		writeEnvFile(t, dir, "CLAUDE_CODE_OAUTH_TOKEN="+token)

		status, _, _ := checkCredentials(dir)
		if status != "warn" {
			t.Errorf("expected warn, got %s", status)
		}
	})

	t.Run("oauth token expired", func(t *testing.T) {
		dir := testSourceDir(t)
		token := makeJWT(t, time.Now().Add(-24*time.Hour).Unix())
		writeEnvFile(t, dir, "CLAUDE_CODE_OAUTH_TOKEN="+token)

		status, _, _ := checkCredentials(dir)
		if status != "fail" {
			t.Errorf("expected fail, got %s", status)
		}
	})

	t.Run("api key present", func(t *testing.T) {
		dir := testSourceDir(t)
		writeEnvFile(t, dir, "ANTHROPIC_API_KEY=sk-ant-test-key")

		status, detail, _ := checkCredentials(dir)
		if status != "pass" {
			t.Errorf("expected pass, got %s: %s", status, detail)
		}
	})

	t.Run("no credentials", func(t *testing.T) {
		dir := testSourceDir(t)

		status, _, _ := checkCredentials(dir)
		if status != "fail" {
			t.Errorf("expected fail, got %s", status)
		}
	})
}

// --- Database checks ---

func TestCheckDatabase(t *testing.T) {
	t.Run("healthy database", func(t *testing.T) {
		dir := testSourceDir(t)
		createTestDB(t, dir)
		addTestMessages(t, dir, "test-jid", 100)

		status, detail, _ := checkDatabase(dir)
		if status != "pass" {
			t.Errorf("expected pass, got %s: %s", status, detail)
		}
	})

	t.Run("missing database", func(t *testing.T) {
		dir := testSourceDir(t)

		status, _, _ := checkDatabase(dir)
		if status != "fail" {
			t.Errorf("expected fail, got %s", status)
		}
	})

	t.Run("empty database", func(t *testing.T) {
		dir := testSourceDir(t)
		createTestDB(t, dir)

		status, _, _ := checkDatabase(dir)
		if status != "pass" {
			t.Errorf("expected pass, got %s", status)
		}
	})
}

// --- Groups checks ---

func TestCheckGroups(t *testing.T) {
	t.Run("consistent groups", func(t *testing.T) {
		dir := testSourceDir(t)
		createTestDB(t, dir)

		// Create group dirs with CLAUDE.md.
		for _, g := range []struct{ name, folder string }{
			{"main", "main"},
			{"dev", "dev"},
		} {
			addTestGroup(t, dir, "jid-"+g.name, g.name, g.folder, g.name == "main")
			gDir := filepath.Join(dir, "groups", g.folder)
			if err := os.MkdirAll(gDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(gDir, "CLAUDE.md"), []byte("# "+g.name), 0o644); err != nil {
				t.Fatal(err)
			}
		}

		status, detail, _ := checkGroups(dir, "")
		if status != "pass" {
			t.Errorf("expected pass, got %s: %s", status, detail)
		}
	})

	t.Run("missing directory", func(t *testing.T) {
		dir := testSourceDir(t)
		createTestDB(t, dir)
		addTestGroup(t, dir, "jid-orphan", "orphan", "orphan", false)

		status, _, _ := checkGroups(dir, "")
		if status != "warn" {
			t.Errorf("expected warn, got %s", status)
		}
	})

	t.Run("missing CLAUDE.md", func(t *testing.T) {
		dir := testSourceDir(t)
		createTestDB(t, dir)
		addTestGroup(t, dir, "jid-nomd", "nomd", "nomd", false)
		if err := os.MkdirAll(filepath.Join(dir, "groups", "nomd"), 0o755); err != nil {
			t.Fatal(err)
		}

		status, _, _ := checkGroups(dir, "")
		if status != "warn" {
			t.Errorf("expected warn, got %s", status)
		}
	})

	t.Run("empty JID", func(t *testing.T) {
		dir := testSourceDir(t)
		createTestDB(t, dir)

		// Insert a group with empty JID directly (bypasses normal validation).
		dbPath := filepath.Join(dir, "store", "messages.db")
		db, err := sql.Open("sqlite", dbPath)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = db.Close() }()
		if _, err := db.Exec(`INSERT INTO registered_groups (jid, name, folder, trigger_pattern, requires_trigger, is_main)
			VALUES ('', 'broken', 'broken', '', 0, 0)`); err != nil {
			t.Fatal(err)
		}

		status, _, _ := checkGroups(dir, "")
		if status != "fail" {
			t.Errorf("expected fail, got %s", status)
		}
	})

	t.Run("filter by group", func(t *testing.T) {
		dir := testSourceDir(t)
		createTestDB(t, dir)
		addTestGroup(t, dir, "jid-main", "main", "main", true)
		addTestGroup(t, dir, "jid-dev", "dev", "dev", false)
		if err := os.MkdirAll(filepath.Join(dir, "groups", "main"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "groups", "main", "CLAUDE.md"), []byte("# main"), 0o644); err != nil {
			t.Fatal(err)
		}

		status, detail, _ := checkGroups(dir, "main")
		if status != "pass" {
			t.Errorf("expected pass, got %s: %s", status, detail)
		}
	})
}

// --- Disk usage percentage calculation ---

func TestDiskUsagePercent(t *testing.T) {
	// Test against the temp directory — should succeed and return a valid percentage.
	dir := t.TempDir()
	pct, err := diskUsagePercent(dir)
	if err != nil {
		t.Fatalf("diskUsagePercent failed: %v", err)
	}
	if pct < 0 || pct > 100 {
		t.Errorf("expected 0-100, got %d", pct)
	}
}

func TestDiskUsagePercentNonexistent(t *testing.T) {
	_, err := diskUsagePercent("/nonexistent/path/that/does/not/exist")
	if err == nil {
		t.Error("expected error for nonexistent path")
	}
}

// --- Format count ---

func TestFormatCount(t *testing.T) {
	tests := []struct {
		n    int64
		want string
	}{
		{0, "0"},
		{42, "42"},
		{999, "999"},
		{1000, "1,000"},
		{18432, "18,432"},
		{1000000, "1,000,000"},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%d", tt.n), func(t *testing.T) {
			got := formatCount(tt.n)
			if got != tt.want {
				t.Errorf("formatCount(%d) = %q, want %q", tt.n, got, tt.want)
			}
		})
	}
}

// --- Version comparison ---

func TestVersionsBehind(t *testing.T) {
	tests := []struct {
		imageVer string
		archVer  string
		want     int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.0.0", "1.0.1", 1},
		{"1.0.0", "1.3.0", 3},
		{"1.0.0", "2.1.0", 11},
	}
	for _, tt := range tests {
		t.Run(tt.imageVer+"→"+tt.archVer, func(t *testing.T) {
			got := versionsBehind(tt.imageVer, tt.archVer)
			if got != tt.want {
				t.Errorf("versionsBehind(%q, %q) = %d, want %d", tt.imageVer, tt.archVer, got, tt.want)
			}
		})
	}
}

// --- handleHealth integration test ---

func TestHandleHealthOutput(t *testing.T) {
	// Capture write() output by redirecting stdout.
	dir := testSourceDir(t)
	createTestDB(t, dir)
	addTestMessages(t, dir, "test-jid", 5)
	addTestGroup(t, dir, "jid-main", "main", "main", true)
	if err := os.MkdirAll(filepath.Join(dir, "groups", "main"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "groups", "main", "CLAUDE.md"), []byte("# main"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeEnvFile(t, dir, "ANTHROPIC_API_KEY=sk-ant-test-key")

	// Build the request message.
	msg := map[string]interface{}{
		"type":       "health_request",
		"source_dir": dir,
		"group":      "",
		"checks":     []interface{}{"credentials", "database", "groups"},
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
	lines := splitNDJSON(output)
	if len(lines) < 4 { // 3 check_results + 1 health_complete
		t.Fatalf("expected at least 4 NDJSON lines, got %d:\n%s", len(lines), output)
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
			status, _ := msg["status"].(string)
			if status != "pass" && status != "warn" && status != "fail" {
				t.Errorf("check %s has invalid status: %s", name, status)
			}
		}
	}

	for _, expected := range []string{"credentials", "database", "groups"} {
		if !checkNames[expected] {
			t.Errorf("missing check_result for %s", expected)
		}
	}
}

func splitNDJSON(s string) []string {
	var lines []string
	for _, line := range splitLines(s) {
		line = trimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
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

// --- Skills check tests ---

// createSkillDir creates a skill with a SKILL.md containing allowed-tools.
func createSkillDir(t *testing.T, dir, sessionName, skillName, allowedTools string) {
	t.Helper()
	skillDir := filepath.Join(dir, "data", "sessions", sessionName, ".claude", "skills", skillName)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := fmt.Sprintf("---\nname: %s\ndescription: test skill\n", skillName)
	if allowedTools != "" {
		content += fmt.Sprintf("allowed-tools: %s\n", allowedTools)
	}
	content += "---\n\n# Test skill\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// createSkillJSON creates a skill.json with mcp_tools.
func createSkillJSON(t *testing.T, dir, sessionName, skillName string, mcpTools []string) {
	t.Helper()
	skillDir := filepath.Join(dir, "data", "sessions", sessionName, ".claude", "skills", skillName)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	doc := map[string]interface{}{
		"name": skillName,
		"nanoclaw": map[string]interface{}{
			"mcp_tools": mcpTools,
		},
	}
	data, _ := json.Marshal(doc)
	if err := os.WriteFile(filepath.Join(skillDir, "skill.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// createSessionJSONL creates a JSONL session file with tool_use entries.
func createSessionJSONL(t *testing.T, dir, sessionName string, toolNames []string) {
	t.Helper()
	projectDir := filepath.Join(dir, "data", "sessions", sessionName, ".claude", "projects", "-workspace-group")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, name := range toolNames {
		entry := map[string]interface{}{
			"message": map[string]interface{}{
				"content": []map[string]interface{}{
					{"type": "tool_use", "name": name, "id": "toolu_test"},
				},
			},
		}
		data, _ := json.Marshal(entry)
		lines = append(lines, string(data))
	}
	if err := os.WriteFile(
		filepath.Join(projectDir, "test-session.jsonl"),
		[]byte(strings.Join(lines, "\n")+"\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
}

func TestCheckSkillsNoSkills(t *testing.T) {
	dir := testSourceDir(t)
	status, detail, _ := checkSkills(dir, "")
	if status != "pass" {
		t.Errorf("expected pass, got %s", status)
	}
	if detail != "no sessions directory" && detail != "no skills installed" {
		t.Errorf("unexpected detail: %s", detail)
	}
}

func TestCheckSkillsPass(t *testing.T) {
	dir := testSourceDir(t)
	createSkillDir(t, dir, "main", "browser", "Bash(browser:*)")
	createSkillDir(t, dir, "main", "status", "")

	status, detail, _ := checkSkills(dir, "")
	if status != "pass" {
		t.Errorf("expected pass, got %s: %s", status, detail)
	}
	if !strings.Contains(detail, "2 skills") {
		t.Errorf("expected '2 skills' in detail, got: %s", detail)
	}
}

func TestCheckSkillsCollision(t *testing.T) {
	dir := testSourceDir(t)
	// Two skills both declare Bash as an allowed tool.
	createSkillDir(t, dir, "main", "skill-a", "Bash(a:*)")
	createSkillDir(t, dir, "main", "skill-b", "Bash(b:*)")

	status, detail, remediation := checkSkills(dir, "")
	if status != "fail" {
		t.Errorf("expected fail, got %s: %s", status, detail)
	}
	if !strings.Contains(detail, "tool name collision") {
		t.Errorf("expected 'tool name collision' in detail, got: %s", detail)
	}
	if !strings.Contains(detail, "Bash") {
		t.Errorf("expected 'Bash' in detail, got: %s", detail)
	}
	if remediation == "" {
		t.Error("expected remediation for collision")
	}
}

func TestCheckSkillsMCPToolCollision(t *testing.T) {
	dir := testSourceDir(t)
	// Two skills both register the same MCP tool via skill.json.
	createSkillJSON(t, dir, "main", "sec-a", []string{"check_vuln", "scan_ports"})
	createSkillJSON(t, dir, "main", "sec-b", []string{"check_vuln", "list_cves"})

	status, detail, _ := checkSkills(dir, "")
	if status != "fail" {
		t.Errorf("expected fail, got %s: %s", status, detail)
	}
	if !strings.Contains(detail, "check_vuln") {
		t.Errorf("expected 'check_vuln' in detail, got: %s", detail)
	}
}

func TestCheckSkillsOrphanedRef(t *testing.T) {
	dir := testSourceDir(t)
	// Install one skill with one MCP tool.
	createSkillJSON(t, dir, "main", "my-skill", []string{"my_tool"})
	// Session references a tool that isn't installed.
	createSessionJSONL(t, dir, "main", []string{"my_tool", "removed_tool"})

	status, detail, remediation := checkSkills(dir, "")
	if status != "warn" {
		t.Errorf("expected warn, got %s: %s", status, detail)
	}
	if !strings.Contains(detail, "orphaned tool ref") {
		t.Errorf("expected 'orphaned tool ref' in detail, got: %s", detail)
	}
	if !strings.Contains(detail, "removed_tool") {
		t.Errorf("expected 'removed_tool' in detail, got: %s", detail)
	}
	if remediation == "" {
		t.Error("expected remediation for orphaned ref")
	}
}

func TestCheckSkillsBuiltinToolsNotFlagged(t *testing.T) {
	dir := testSourceDir(t)
	createSkillDir(t, dir, "main", "my-skill", "")
	// Session only references builtin tools — should not be flagged.
	createSessionJSONL(t, dir, "main", []string{"Bash", "Read", "Write", "Edit"})

	status, detail, _ := checkSkills(dir, "")
	if status != "pass" {
		t.Errorf("expected pass (builtins should not be flagged), got %s: %s", status, detail)
	}
}

func TestCheckSkillsFilterGroup(t *testing.T) {
	dir := testSourceDir(t)
	createSkillDir(t, dir, "main", "skill-a", "Bash(a:*)")
	createSkillDir(t, dir, "main", "skill-b", "Bash(b:*)")
	createSkillDir(t, dir, "dev", "skill-c", "")

	// Filter to "dev" — should not see the collision in "main".
	status, detail, _ := checkSkills(dir, "dev")
	if status != "pass" {
		t.Errorf("expected pass for dev group, got %s: %s", status, detail)
	}

	// Filter to "main" — should see the collision.
	status, detail, _ = checkSkills(dir, "main")
	if status != "fail" {
		t.Errorf("expected fail for main group, got %s: %s", status, detail)
	}
}

func TestParseSkillMDTools(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")

	// Multiple tools.
	content := "---\nname: test\nallowed-tools: Bash(x:*), Read, WebFetch\n---\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	tools := parseSkillMDTools(path)
	if len(tools) != 3 {
		t.Fatalf("expected 3 tools, got %d: %v", len(tools), tools)
	}
	if tools[0] != "Bash" || tools[1] != "Read" || tools[2] != "WebFetch" {
		t.Errorf("unexpected tools: %v", tools)
	}
}

func TestParseSkillMDToolsNoAllowedTools(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")
	content := "---\nname: test\ndescription: no tools\n---\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	tools := parseSkillMDTools(path)
	if len(tools) != 0 {
		t.Errorf("expected 0 tools, got %v", tools)
	}
}

func TestParseSkillJSONTools(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "skill.json")
	doc := map[string]interface{}{
		"name": "test",
		"nanoclaw": map[string]interface{}{
			"mcp_tools": []string{"tool_a", "tool_b"},
		},
	}
	data, _ := json.Marshal(doc)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	tools := parseSkillJSONTools(path)
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}
	if tools[0] != "tool_a" || tools[1] != "tool_b" {
		t.Errorf("unexpected tools: %v", tools)
	}
}

func TestParseSkillJSONToolsMissing(t *testing.T) {
	tools := parseSkillJSONTools("/nonexistent/skill.json")
	if len(tools) != 0 {
		t.Errorf("expected 0 tools for missing file, got %v", tools)
	}
}
