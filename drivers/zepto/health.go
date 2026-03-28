// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func handleHealth(msg map[string]interface{}) {
	sourceDir, _ := msg["source_dir"].(string)
	group, _ := msg["group"].(string)
	_ = group // zepto does not have nanoclaw-style groups

	dataDir := findDataDir()
	if sourceDir != "" {
		if _, err := os.Stat(filepath.Join(sourceDir, "config.json")); err == nil {
			dataDir = sourceDir
		}
	}

	// Determine which checks to run.
	var requested []string
	if raw, ok := msg["checks"].([]interface{}); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok {
				requested = append(requested, s)
			}
		}
	}

	allChecks := []string{"runtime", "credentials", "disk", "sessions", "database", "groups", "skills", "image"}
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
			status, detail, remediation = checkZeptoRuntime()
		case "credentials":
			status, detail, remediation = checkZeptoCredentials(dataDir)
		case "disk":
			status, detail, remediation = checkZeptoDisk(dataDir)
		case "sessions":
			status, detail, remediation = checkZeptoSessions(dataDir)
		case "database", "groups", "skills", "image":
			status, detail = "pass", "not applicable to zepto"
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

func checkZeptoRuntime() (status, detail, remediation string) {
	bin := findBinary()
	out, err := exec.Command(bin, "version").Output()
	if err != nil {
		return "fail", "zeptoclaw binary not found or not responding",
			"Install zeptoclaw or set ZEPTOCLAW_BIN"
	}
	version := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])

	// Also check container runtime if available.
	rt := detectRuntime()
	if rt != "" {
		return "pass", fmt.Sprintf("%s, %s available", version, rt), ""
	}
	return "pass", version, ""
}

func checkZeptoCredentials(dataDir string) (status, detail, remediation string) {
	// Check environment variables first.
	if token := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"); token != "" {
		// Try to parse as JWT for expiry checking.
		expiry, err := zeptoJWTExpiry(token)
		if err != nil {
			// Not a JWT — could be an opaque access token (e.g. sk-ant-oat01-*).
			return "pass", "CLAUDE_CODE_OAUTH_TOKEN present (expiry unknown — use --ping to validate)", ""
		}
		remaining := time.Until(expiry)
		if remaining <= 0 {
			return "fail", "CLAUDE_CODE_OAUTH_TOKEN expired",
				"Re-authenticate to get a fresh OAuth token"
		}
		days := int(remaining.Hours() / 24)
		if days <= 7 {
			return "warn", fmt.Sprintf("CLAUDE_CODE_OAUTH_TOKEN valid (expires in %dd)", days),
				"Re-authenticate soon to refresh your OAuth token"
		}
		return "pass", fmt.Sprintf("CLAUDE_CODE_OAUTH_TOKEN valid (expires in %dd)", days), ""
	}

	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		return "pass", "ANTHROPIC_API_KEY present", ""
	}

	// Check config.json for stored credentials.
	configPath := filepath.Join(dataDir, "config.json")
	data, err := os.ReadFile(configPath)
	if err == nil {
		var cfg map[string]interface{}
		if json.Unmarshal(data, &cfg) == nil {
			if _, ok := cfg["api_key"]; ok {
				return "pass", "API key found in config.json", ""
			}
		}
	}

	return "fail", "no API credentials found",
		"Set CLAUDE_CODE_OAUTH_TOKEN or ANTHROPIC_API_KEY, or configure in config.json"
}

// zeptoJWTExpiry decodes a JWT and returns the expiry time from the exp claim.
func zeptoJWTExpiry(token string) (time.Time, error) {
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

func checkZeptoDisk(dataDir string) (status, detail, remediation string) {
	pct, err := zeptoDiskUsagePercent(dataDir)
	if err != nil {
		return "fail", fmt.Sprintf("cannot check disk: %v", err),
			"Ensure the ZeptoClaw data directory exists"
	}

	detailStr := fmt.Sprintf("data dir %d%% full (%s)", pct, dataDir)
	if pct > 90 {
		return "fail", detailStr, "Free up disk space"
	}
	if pct >= 80 {
		return "warn", detailStr, "Disk space is getting low"
	}
	return "pass", fmt.Sprintf("data dir %d%% full", pct), ""
}

func zeptoDiskUsagePercent(path string) (int, error) {
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

func checkZeptoSessions(dataDir string) (status, detail, remediation string) {
	instances := gatherInstances()
	active := len(instances)

	// Check for stale session files (no update in >10 minutes).
	sessDir := filepath.Join(dataDir, "sessions")
	entries, err := os.ReadDir(sessDir)
	if err != nil {
		// No sessions directory is fine.
		return "pass", fmt.Sprintf("%d active, 0 stuck", active), ""
	}

	staleThreshold := time.Now().Add(-10 * time.Minute)
	var stale int
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(staleThreshold) && info.Size() > 0 {
			stale++
		}
	}

	if stale > 0 {
		return "warn",
			fmt.Sprintf("%d active, %d stale session files", active, stale),
			"Old session files may be cleaned up"
	}
	return "pass", fmt.Sprintf("%d active, 0 stuck", active), ""
}
