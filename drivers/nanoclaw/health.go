// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

func handleHealth(msg map[string]interface{}) {
	sourceDir, _ := msg["source_dir"].(string)
	if sourceDir == "" {
		sourceDir = findSourceDir()
	}
	group, _ := msg["group"].(string)

	// Determine which checks to run.
	var requested []string
	if raw, ok := msg["checks"].([]interface{}); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok {
				requested = append(requested, s)
			}
		}
	}

	allChecks := []string{"runtime", "credentials", "database", "disk", "sessions", "groups", "skills", "image"}
	if len(requested) == 0 {
		requested = allChecks
	}

	checkSet := map[string]bool{}
	for _, c := range requested {
		checkSet[c] = true
	}

	var pass, warn, fail int

	for _, name := range allChecks {
		if !checkSet[name] {
			continue
		}
		var status, detail, remediation string
		switch name {
		case "runtime":
			status, detail, remediation = checkRuntime()
		case "credentials":
			status, detail, remediation = checkCredentials(sourceDir)
		case "database":
			status, detail, remediation = checkDatabase(sourceDir)
		case "disk":
			status, detail, remediation = checkDisk(sourceDir)
		case "sessions":
			status, detail, remediation = checkSessions()
		case "groups":
			status, detail, remediation = checkGroups(sourceDir, group)
		case "skills":
			status, detail, remediation = checkSkills(sourceDir, group)
		case "image":
			status, detail, remediation = checkImage(sourceDir)
		}

		result := map[string]interface{}{
			"type":   "check_result",
			"name":   name,
			"status": status,
			"detail": detail,
		}
		if remediation != "" {
			result["remediation"] = remediation
		}
		write(result)

		switch status {
		case "pass":
			pass++
		case "warn":
			warn++
		case "fail":
			fail++
		}
	}

	write(map[string]interface{}{
		"type": "health_complete",
		"pass": pass,
		"warn": warn,
		"fail": fail,
	})
}

func checkRuntime() (status, detail, remediation string) {
	rt := detectRuntime()
	if rt == "" {
		return "fail", "neither 'container' nor 'docker' found", "Install Docker or Apple Containers"
	}
	// Get version info.
	var out []byte
	var err error
	if rt == "container" {
		out, err = exec.Command("container", "--version").Output()
	} else {
		out, err = exec.Command("docker", "version", "--format", "{{.Server.Version}}").Output()
	}
	if err != nil {
		return "fail", fmt.Sprintf("%s found but not responding", rt), "Check that the container runtime is running"
	}
	version := strings.TrimSpace(string(out))
	if rt == "container" {
		// Parse "container CLI version 0.9.0 (build: ...)" to extract version.
		if idx := strings.Index(version, "version "); idx >= 0 {
			v := version[idx+len("version "):]
			if sp := strings.IndexAny(v, " ("); sp >= 0 {
				v = v[:sp]
			}
			version = v
		}
	}
	return "pass", fmt.Sprintf("%s %s", rt, version), ""
}

func checkCredentials(sourceDir string) (status, detail, remediation string) {
	secrets := readSecrets(sourceDir)

	// Check OAuth token first, then API key.
	if token, ok := secrets["CLAUDE_CODE_OAUTH_TOKEN"]; ok && token != "" {
		// Try to parse as JWT for expiry checking.
		expiry, err := jwtExpiry(token)
		if err != nil {
			// Not a JWT — could be an opaque access token (e.g. sk-ant-oat01-*).
			// Token is present, but we can't check expiry without --ping.
			return "pass", "CLAUDE_CODE_OAUTH_TOKEN present (expiry unknown — use --ping to validate)", ""
		}
		remaining := time.Until(expiry)
		if remaining <= 0 {
			return "fail", "CLAUDE_CODE_OAUTH_TOKEN expired", "Re-authenticate to get a fresh OAuth token"
		}
		days := int(remaining.Hours() / 24)
		if days <= 7 {
			return "warn", fmt.Sprintf("CLAUDE_CODE_OAUTH_TOKEN valid (expires in %dd)", days),
				"Re-authenticate soon to refresh your OAuth token"
		}
		return "pass", fmt.Sprintf("CLAUDE_CODE_OAUTH_TOKEN valid (expires in %dd)", days), ""
	}

	if key, ok := secrets["ANTHROPIC_API_KEY"]; ok && key != "" {
		return "pass", "ANTHROPIC_API_KEY present", ""
	}

	return "fail", "no API credentials found", "Set CLAUDE_CODE_OAUTH_TOKEN or ANTHROPIC_API_KEY in .env"
}

// jwtExpiry decodes a JWT and returns the expiry time from the exp claim.
func jwtExpiry(token string) (time.Time, error) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return time.Time{}, fmt.Errorf("not a JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid base64: %w", err)
	}
	var claims struct {
		Exp float64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return time.Time{}, fmt.Errorf("invalid JSON: %w", err)
	}
	if claims.Exp == 0 {
		return time.Time{}, fmt.Errorf("no exp claim")
	}
	return time.Unix(int64(claims.Exp), 0), nil
}

func checkDatabase(sourceDir string) (status, detail, remediation string) {
	dbPath := filepath.Join(sourceDir, "store", "messages.db")
	info, err := os.Stat(dbPath)
	if err != nil {
		return "fail", "messages.db not found", "Ensure NanoClaw is installed and has run at least once"
	}

	sizeMB := info.Size() / (1024 * 1024)

	db, err := openDB(sourceDir)
	if err != nil {
		return "fail", fmt.Sprintf("cannot open messages.db: %v", err), "Check file permissions on messages.db"
	}
	defer func() { _ = db.Close() }()

	// Integrity check.
	var integrity string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil {
		return "fail", fmt.Sprintf("integrity check error: %v", err), "Database may be corrupted; consider restoring from backup"
	}
	if integrity != "ok" {
		return "fail", fmt.Sprintf("integrity check failed: %s", integrity), "Database is corrupted; restore from backup"
	}

	// Row count.
	var rowCount int64
	if err := db.QueryRow("SELECT count(*) FROM messages").Scan(&rowCount); err != nil {
		return "fail", fmt.Sprintf("cannot query messages: %v", err), "Check database schema"
	}

	detailStr := fmt.Sprintf("ok (messages.db %dMB, %s rows)", sizeMB, formatCount(rowCount))
	if sizeMB > 1024 {
		return "warn", detailStr, "Database is large and may impact performance; consider archiving old messages"
	}
	return "pass", detailStr, ""
}

// formatCount formats a number with commas (e.g., 18432 → "18,432").
func formatCount(n int64) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var result []byte
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, byte(c))
	}
	return string(result)
}

func checkDisk(sourceDir string) (status, detail, remediation string) {
	groupDir := filepath.Join(sourceDir, "groups")
	pct, err := diskUsagePercent(groupDir)
	if err != nil {
		// Try the parent sourceDir if groups dir doesn't exist.
		pct, err = diskUsagePercent(sourceDir)
		if err != nil {
			return "fail", fmt.Sprintf("cannot check disk: %v", err), "Ensure the installation directory exists"
		}
	}

	detailStr := fmt.Sprintf("group dir %d%% full (%s)", pct, groupDir)
	if pct > 90 {
		return "fail", detailStr, "Free up space or move groups to a larger volume"
	}
	if pct >= 80 {
		return "warn", detailStr, "Disk space is getting low; consider freeing up space"
	}
	return "pass", fmt.Sprintf("group dir %d%% full", pct), ""
}

// diskUsagePercent returns the percentage of disk used on the filesystem
// containing the given path.
func diskUsagePercent(path string) (int, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	total := stat.Blocks * uint64(stat.Bsize)
	free := stat.Bavail * uint64(stat.Bsize)
	if total == 0 {
		return 0, fmt.Errorf("filesystem reports 0 total blocks")
	}
	used := total - free
	pct := int(used * 100 / total)
	return pct, nil
}

func checkSessions() (status, detail, remediation string) {
	rt := detectRuntime()
	if rt == "" {
		return "pass", "0 active (no container runtime)", ""
	}

	containers := fetchContainers(rt)
	active := 0
	var stuck []string

	for _, c := range containers {
		if !strings.HasPrefix(c.id, "nanoclaw-") {
			continue
		}
		if c.state != "running" {
			continue
		}
		active++

		// Check if container has produced output recently.
		if isStuck(rt, c.id) {
			stuck = append(stuck, c.id)
		}
	}

	if len(stuck) > 0 {
		return "fail",
			fmt.Sprintf("%d active, %d stuck", active, len(stuck)),
			fmt.Sprintf("Stuck sessions: %s — consider restarting them", strings.Join(stuck, ", "))
	}
	return "pass", fmt.Sprintf("%d active, 0 stuck", active), ""
}

// isStuck checks if a container has produced no stdout in >10 minutes.
func isStuck(runtime, containerID string) bool {
	var out []byte
	var err error
	if runtime == "docker" {
		out, err = exec.Command("docker", "logs", "--since", "10m", "--tail", "1", containerID).Output()
	} else {
		// Apple Containers: no --since flag, so skip stuck detection.
		return false
	}
	if err != nil {
		return false
	}
	return len(strings.TrimSpace(string(out))) == 0
}

func checkGroups(sourceDir, filterGroup string) (status, detail, remediation string) {
	groups, err := readGroupRows(sourceDir)
	if err != nil {
		return "fail", fmt.Sprintf("cannot read groups: %v", err), "Check database access"
	}

	if filterGroup != "" {
		g, err := findGroup(groups, filterGroup)
		if err != nil {
			return "fail", fmt.Sprintf("group not found: %v", err), ""
		}
		groups = []GroupRow{*g}
	}

	// Check for empty or duplicate JIDs.
	jidSeen := map[string]string{}
	var problems []string
	for _, g := range groups {
		if g.JID == "" {
			problems = append(problems, fmt.Sprintf("%s has empty JID", g.Name))
			continue
		}
		if prev, ok := jidSeen[g.JID]; ok {
			problems = append(problems, fmt.Sprintf("%s and %s share JID %s", prev, g.Name, g.JID))
		}
		jidSeen[g.JID] = g.Name
	}
	if len(problems) > 0 {
		return "fail",
			fmt.Sprintf("JID problems: %s", strings.Join(problems, "; ")),
			"Fix JID issues in the database"
	}

	// Check each group has a directory and CLAUDE.md.
	var warnings []string
	for _, g := range groups {
		gDir := filepath.Join(sourceDir, "groups", g.Folder)
		if _, err := os.Stat(gDir); err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: missing directory", g.Name))
			continue
		}
		claudeMD := filepath.Join(gDir, "CLAUDE.md")
		if _, err := os.Stat(claudeMD); err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: missing CLAUDE.md", g.Name))
		}
	}

	names := make([]string, len(groups))
	for i, g := range groups {
		names[i] = g.Name
	}
	groupList := strings.Join(names, ", ")

	if len(warnings) > 0 {
		return "warn",
			fmt.Sprintf("%d registered (%s); %s", len(groups), groupList, strings.Join(warnings, "; ")),
			"Create missing group directories or CLAUDE.md files"
	}
	return "pass", fmt.Sprintf("%d registered (%s)", len(groups), groupList), ""
}

func checkImage(sourceDir string) (status, detail, remediation string) {
	rt := detectRuntime()
	if rt == "" {
		return "pass", "no container runtime (skipped)", ""
	}
	if rt == "container" {
		return checkImageAppleContainers(sourceDir)
	}

	// Get the local image digest.
	out, err := exec.Command("docker", "images", "nanoclaw-agent:latest", "--format", "{{.ID}}").Output()
	if err != nil || strings.TrimSpace(string(out)) == "" {
		return "fail", "nanoclaw-agent:latest not found locally",
			"Pull or build the nanoclaw-agent image"
	}

	// Compare the arch version with what's in the image labels if available.
	archVersion := detectArchVersion(sourceDir)
	labelOut, err := exec.Command("docker", "inspect", "--format", "{{index .Config.Labels \"version\"}}", "nanoclaw-agent:latest").Output()
	if err != nil {
		return "pass", fmt.Sprintf("nanoclaw-agent:latest present (arch %s)", archVersion), ""
	}
	imageVersion := strings.TrimSpace(string(labelOut))
	if imageVersion == "" || imageVersion == archVersion {
		return "pass", fmt.Sprintf("nanoclaw-agent:latest matches arch version %s", archVersion), ""
	}

	behind := versionsBehind(imageVersion, archVersion)
	detailStr := fmt.Sprintf("nanoclaw-agent:latest is %d versions behind (image=%s, arch=%s)", behind, imageVersion, archVersion)
	if behind > 5 {
		return "fail", detailStr, "Rebuild or pull the latest nanoclaw-agent image"
	}
	if behind > 0 {
		return "warn", detailStr, "Consider rebuilding the nanoclaw-agent image"
	}
	return "pass", fmt.Sprintf("nanoclaw-agent:latest matches arch version %s", archVersion), ""
}

// checkImageAppleContainers queries Apple Containers for the image name and created date.
func checkImageAppleContainers(sourceDir string) (status, detail, remediation string) {
	out, err := exec.Command("container", "list", "--format", "json").Output()
	if err != nil {
		return "pass", "Apple Containers (could not query)", ""
	}

	var containers []struct {
		Configuration struct {
			Image struct {
				Reference  string `json:"reference"`
				Descriptor struct {
					Annotations map[string]string `json:"annotations"`
				} `json:"descriptor"`
			} `json:"image"`
			ID string `json:"id"`
		} `json:"configuration"`
		Status string `json:"status"`
	}
	if json.Unmarshal(out, &containers) != nil {
		return "pass", "Apple Containers (could not parse)", ""
	}

	// Find a running nanoclaw container
	for _, c := range containers {
		ref := c.Configuration.Image.Reference
		if c.Status != "running" || !strings.Contains(ref, "nanoclaw-agent") {
			continue
		}
		created := c.Configuration.Image.Descriptor.Annotations["org.opencontainers.image.created"]
		if created != "" {
			return "pass", fmt.Sprintf("%s (built %s)", ref, created), ""
		}
		return "pass", ref, ""
	}

	archVersion := detectArchVersion(sourceDir)
	return "pass", fmt.Sprintf("no running nanoclaw container (arch %s)", archVersion), ""
}

// versionsBehind does a simple semver minor+patch distance estimate.
func versionsBehind(imageVer, archVer string) int {
	iv := parseVersionParts(imageVer)
	av := parseVersionParts(archVer)
	if len(iv) < 3 || len(av) < 3 {
		return 0
	}
	// Major version difference is huge.
	if av[0] > iv[0] {
		return (av[0]-iv[0])*10 + av[1]
	}
	if av[1] > iv[1] {
		return av[1] - iv[1]
	}
	if av[2] > iv[2] {
		return 1
	}
	return 0
}

func parseVersionParts(v string) []int {
	v = strings.TrimPrefix(v, "v")
	parts := strings.Split(v, ".")
	var nums []int
	for _, p := range parts {
		var n int
		_, _ = fmt.Sscanf(p, "%d", &n)
		nums = append(nums, n)
	}
	return nums
}

func checkSkills(sourceDir, filterGroup string) (status, detail, remediation string) {
	sessionsDir := filepath.Join(sourceDir, "data", "sessions")
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return "pass", "no sessions directory", ""
	}

	var collisions []string
	var orphaned []string
	totalSkills := 0

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sessionName := e.Name()
		if filterGroup != "" && !strings.EqualFold(sessionName, filterGroup) {
			continue
		}

		skillsDir := filepath.Join(sessionsDir, sessionName, ".claude", "skills")
		skillEntries, err := os.ReadDir(skillsDir)
		if err != nil {
			continue // no skills dir for this session
		}

		// Collect tool → skill mappings for this session.
		toolOwners := map[string][]string{} // tool name → []skill names
		allTools := map[string]bool{}

		for _, se := range skillEntries {
			if !se.IsDir() {
				continue
			}
			skillName := se.Name()
			totalSkills++
			skillPath := filepath.Join(skillsDir, skillName)

			// Parse SKILL.md for allowed-tools.
			tools := parseSkillMDTools(filepath.Join(skillPath, "SKILL.md"))
			for _, t := range tools {
				toolOwners[t] = append(toolOwners[t], skillName)
				allTools[t] = true
			}

			// Parse skill.json for mcp_tools.
			mcpTools := parseSkillJSONTools(filepath.Join(skillPath, "skill.json"))
			for _, t := range mcpTools {
				toolOwners[t] = append(toolOwners[t], skillName)
				allTools[t] = true
			}
		}

		// Check for collisions.
		for tool, owners := range toolOwners {
			if len(owners) > 1 {
				// Deduplicate owner names.
				seen := map[string]bool{}
				var unique []string
				for _, o := range owners {
					if !seen[o] {
						seen[o] = true
						unique = append(unique, o)
					}
				}
				if len(unique) > 1 {
					sort.Strings(unique)
					collisions = append(collisions,
						fmt.Sprintf("tool name collision: %q registered by both %q and %q in group %q",
							tool, unique[0], unique[1], sessionName))
				}
			}
		}

		// Check for orphaned tool refs in session JSONL files.
		orphanedRefs := findOrphanedToolRefs(
			filepath.Join(sessionsDir, sessionName, ".claude", "projects"),
			allTools, sessionName,
		)
		orphaned = append(orphaned, orphanedRefs...)
	}

	if len(collisions) > 0 {
		return "fail", collisions[0],
			"rm -rf data/sessions/<group>/.claude/skills/<skill-name>/ to remove the conflicting skill"
	}
	if len(orphaned) > 0 {
		return "warn", orphaned[0],
			"rm -rf data/sessions/<group>/.claude/ to start a clean session (group memory is preserved)"
	}
	if totalSkills == 0 {
		return "pass", "no skills installed", ""
	}
	return "pass", fmt.Sprintf("%d skills installed, no conflicts", totalSkills), ""
}

// parseSkillMDTools extracts tool names from a SKILL.md's allowed-tools frontmatter.
// Format: "allowed-tools: Bash(skillname:*)" → returns ["Bash"]
func parseSkillMDTools(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	inFrontmatter := false
	var tools []string

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "---" {
			if inFrontmatter {
				break // end of frontmatter
			}
			inFrontmatter = true
			continue
		}
		if !inFrontmatter {
			continue
		}
		if strings.HasPrefix(line, "allowed-tools:") {
			value := strings.TrimPrefix(line, "allowed-tools:")
			value = strings.TrimSpace(value)
			// Parse comma-separated tool declarations like "Bash(x:*), Read, Write"
			for _, decl := range strings.Split(value, ",") {
				decl = strings.TrimSpace(decl)
				if decl == "" {
					continue
				}
				// Extract tool name before any parenthesized qualifier.
				if idx := strings.Index(decl, "("); idx > 0 {
					decl = decl[:idx]
				}
				tools = append(tools, strings.TrimSpace(decl))
			}
		}
	}
	return tools
}

// parseSkillJSONTools extracts MCP tool names from a skill.json's nanoclaw.mcp_tools array.
func parseSkillJSONTools(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var doc struct {
		NanoClaw struct {
			MCPTools []string `json:"mcp_tools"`
		} `json:"nanoclaw"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil
	}
	return doc.NanoClaw.MCPTools
}

// findOrphanedToolRefs scans JSONL session files for tool_use blocks that reference
// tools not in the installed set.
func findOrphanedToolRefs(projectsDir string, installedTools map[string]bool, sessionName string) []string {
	if len(installedTools) == 0 {
		return nil
	}

	// Find the most recent JSONL file.
	var newest string
	var newestTime time.Time

	_ = filepath.Walk(projectsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		if info.ModTime().After(newestTime) {
			newest = path
			newestTime = info.ModTime()
		}
		return nil
	})

	if newest == "" {
		return nil
	}

	f, err := os.Open(newest)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()

	// Scan for tool_use blocks and collect referenced tool names.
	referencedTools := map[string]bool{}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		// Quick check to avoid unmarshaling every line.
		if !strings.Contains(string(line), "tool_use") {
			continue
		}
		var entry struct {
			Message struct {
				Content []struct {
					Type string `json:"type"`
					Name string `json:"name"`
				} `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}
		for _, c := range entry.Message.Content {
			if c.Type == "tool_use" && c.Name != "" {
				referencedTools[c.Name] = true
			}
		}
	}

	// Check each referenced tool against installed set.
	// Only flag tools that look like skill-provided tools (contain underscore or
	// are prefixed with a skill name pattern), not built-in tools.
	builtinTools := map[string]bool{
		"Bash": true, "Read": true, "Write": true, "Edit": true,
		"MultiEdit": true, "Glob": true, "Grep": true, "Agent": true,
		"Task": true, "TaskCreate": true, "TaskUpdate": true,
		"TaskGet": true, "TaskList": true, "TaskOutput": true,
		"TaskStop": true, "TodoWrite": true, "TodoRead": true,
		"WebFetch": true, "WebSearch": true, "NotebookEdit": true,
		"LSP": true, "EnterPlanMode": true, "ExitPlanMode": true,
		"EnterWorktree": true, "ExitWorktree": true,
		"AskUserQuestion": true, "Skill": true, "ToolSearch": true,
		"CronCreate": true, "CronList": true, "CronDelete": true,
		"RemoteTrigger": true,
	}

	var orphaned []string
	for tool := range referencedTools {
		if builtinTools[tool] {
			continue
		}
		// Platform-provided MCP tools (mcp__nanoclaw__*) are not skills.
		if strings.HasPrefix(tool, "mcp__") {
			continue
		}
		if !installedTools[tool] {
			orphaned = append(orphaned, fmt.Sprintf(
				"orphaned tool ref: %q in %s/.claude/*.jsonl — skill not installed",
				tool, sessionName))
		}
	}
	return orphaned
}
