// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// findDataDir locates the ZeptoClaw data directory.
// Resolution: ZEPTOCLAW_DIR env → ~/.zeptoclaw
func findDataDir() string {
	if env := os.Getenv("ZEPTOCLAW_DIR"); env != "" {
		return env
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".zeptoclaw")
}

// findBinary locates the zeptoclaw binary.
// Resolution: ZEPTOCLAW_BIN env → PATH lookup → common locations.
func findBinary() string {
	if env := os.Getenv("ZEPTOCLAW_BIN"); env != "" {
		return env
	}
	if path, err := exec.LookPath("zeptoclaw"); err == nil {
		return path
	}
	home, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(home, ".local", "bin", "zeptoclaw"),
		"/usr/local/bin/zeptoclaw",
		"/opt/homebrew/bin/zeptoclaw",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return "zeptoclaw"
}

// probeZeptoClaw returns confidence (0.0–1.0) that sourceDir/dataDir is a ZeptoClaw install.
// sourceDir is the installation root (unused for ZeptoClaw which uses ~/.zeptoclaw),
// but checked if explicitly provided.
func probeZeptoClaw(sourceDir string) float64 {
	dataDir := findDataDir()
	if sourceDir != "" {
		// If caller provided a dir, check if it looks like a zepto data dir
		if _, err := os.Stat(filepath.Join(sourceDir, "config.json")); err == nil {
			dataDir = sourceDir
		}
	}

	checks := []struct {
		path   string
		weight float64
	}{
		{filepath.Join(dataDir, "config.json"), 0.5},
		{filepath.Join(dataDir, "sessions"), 0.2},
		{filepath.Join(dataDir, "memory"), 0.1},
		{filepath.Join(dataDir, "cron"), 0.1},
	}

	// Also check if the binary exists
	if path, err := exec.LookPath("zeptoclaw"); err == nil && path != "" {
		checks = append(checks, struct {
			path   string
			weight float64
		}{"__binary__", 0.1})
	}

	var score float64
	for _, c := range checks {
		if c.path == "__binary__" {
			score += c.weight
			continue
		}
		if _, err := os.Stat(c.path); err == nil {
			score += c.weight
		}
	}
	return score
}

// detectArchVersion returns the ZeptoClaw version string.
// Tries `zeptoclaw version`, then reads config.json.
func detectArchVersion(sourceDir string) string {
	bin := findBinary()
	out, err := exec.Command(bin, "version").Output()
	if err == nil {
		// Output: "zeptoclaw 0.8.2\n..."
		line := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			return parts[1]
		}
	}

	// Fallback: read version from config.json if present
	dataDir := findDataDir()
	if sourceDir != "" {
		if _, err := os.Stat(filepath.Join(sourceDir, "config.json")); err == nil {
			dataDir = sourceDir
		}
	}
	data, err := os.ReadFile(filepath.Join(dataDir, "config.json"))
	if err != nil {
		return "unknown"
	}
	var cfg struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(data, &cfg) == nil && cfg.Version != "" {
		return cfg.Version
	}
	return "unknown"
}

// readConfig reads key fields from ~/.zeptoclaw/config.json.
type zeptoConfig struct {
	Version  string            `json:"version"`
	Channels map[string]interface{} `json:"channels"`
}

func readConfig(dataDir string) (*zeptoConfig, error) {
	data, err := os.ReadFile(filepath.Join(dataDir, "config.json"))
	if err != nil {
		return nil, err
	}
	var cfg zeptoConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
