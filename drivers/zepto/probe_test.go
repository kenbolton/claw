// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestFindDataDir(t *testing.T) {
	t.Run("uses ZEPTOCLAW_DIR env", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("ZEPTOCLAW_DIR", dir)
		got := findDataDir()
		if got != dir {
			t.Errorf("expected %s, got %s", dir, got)
		}
	})

	t.Run("defaults to ~/.zeptoclaw", func(t *testing.T) {
		t.Setenv("ZEPTOCLAW_DIR", "")
		got := findDataDir()
		home, _ := os.UserHomeDir()
		want := filepath.Join(home, ".zeptoclaw")
		if got != want {
			t.Errorf("expected %s, got %s", want, got)
		}
	})
}

func TestFindBinary(t *testing.T) {
	t.Run("uses ZEPTOCLAW_BIN env", func(t *testing.T) {
		t.Setenv("ZEPTOCLAW_BIN", "/custom/path/zeptoclaw")
		got := findBinary()
		if got != "/custom/path/zeptoclaw" {
			t.Errorf("expected /custom/path/zeptoclaw, got %s", got)
		}
	})

	t.Run("falls back to zeptoclaw when nothing found", func(t *testing.T) {
		t.Setenv("ZEPTOCLAW_BIN", "")
		// If zeptoclaw isn't in PATH or common locations, should return "zeptoclaw"
		got := findBinary()
		// Just verify it returns a non-empty string
		if got == "" {
			t.Error("expected non-empty binary path")
		}
	})
}

func TestProbeZeptoClaw(t *testing.T) {
	t.Run("empty directory returns zero", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("ZEPTOCLAW_DIR", dir)
		score := probeZeptoClaw("")
		if score != 0.0 {
			t.Errorf("expected 0.0 for empty dir, got %f", score)
		}
	})

	t.Run("config.json gives 0.5", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("ZEPTOCLAW_DIR", dir)
		if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{}`), 0o644); err != nil {
			t.Fatal(err)
		}
		score := probeZeptoClaw("")
		if score < 0.49 || score > 0.51 {
			t.Errorf("expected ~0.5, got %f", score)
		}
	})

	t.Run("all directories present gives higher score", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("ZEPTOCLAW_DIR", dir)
		if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{}`), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, sub := range []string{"sessions", "memory", "cron"} {
			if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		score := probeZeptoClaw("")
		// 0.5 + 0.2 + 0.1 + 0.1 = 0.9 (without binary in PATH)
		if score < 0.89 {
			t.Errorf("expected >= 0.9, got %f", score)
		}
	})

	t.Run("explicit sourceDir with config.json overrides dataDir", func(t *testing.T) {
		emptyDir := t.TempDir()
		t.Setenv("ZEPTOCLAW_DIR", emptyDir)

		sourceDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(sourceDir, "config.json"), []byte(`{}`), 0o644); err != nil {
			t.Fatal(err)
		}
		score := probeZeptoClaw(sourceDir)
		if score < 0.49 {
			t.Errorf("expected >= 0.5, got %f", score)
		}
	})

	t.Run("explicit sourceDir without config.json uses dataDir", func(t *testing.T) {
		dataDir := t.TempDir()
		t.Setenv("ZEPTOCLAW_DIR", dataDir)
		if err := os.WriteFile(filepath.Join(dataDir, "config.json"), []byte(`{}`), 0o644); err != nil {
			t.Fatal(err)
		}

		sourceDir := t.TempDir() // no config.json here
		score := probeZeptoClaw(sourceDir)
		// Should fall back to dataDir which has config.json
		if score < 0.49 {
			t.Errorf("expected >= 0.5, got %f", score)
		}
	})
}

func TestDetectArchVersion(t *testing.T) {
	t.Run("reads version from config.json", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("ZEPTOCLAW_DIR", dir)
		// Use a non-existent binary so the command fallback fails
		t.Setenv("ZEPTOCLAW_BIN", "/nonexistent/zeptoclaw")

		cfg := map[string]string{"version": "1.2.3"}
		data, _ := json.Marshal(cfg)
		if err := os.WriteFile(filepath.Join(dir, "config.json"), data, 0o644); err != nil {
			t.Fatal(err)
		}

		got := detectArchVersion("")
		if got != "1.2.3" {
			t.Errorf("expected 1.2.3, got %s", got)
		}
	})

	t.Run("returns unknown when nothing available", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("ZEPTOCLAW_DIR", dir)
		t.Setenv("ZEPTOCLAW_BIN", "/nonexistent/zeptoclaw")

		got := detectArchVersion("")
		if got != "unknown" {
			t.Errorf("expected unknown, got %s", got)
		}
	})

	t.Run("sourceDir config.json overrides dataDir", func(t *testing.T) {
		dataDir := t.TempDir()
		t.Setenv("ZEPTOCLAW_DIR", dataDir)
		t.Setenv("ZEPTOCLAW_BIN", "/nonexistent/zeptoclaw")

		sourceDir := t.TempDir()
		cfg := map[string]string{"version": "4.5.6"}
		data, _ := json.Marshal(cfg)
		if err := os.WriteFile(filepath.Join(sourceDir, "config.json"), data, 0o644); err != nil {
			t.Fatal(err)
		}

		got := detectArchVersion(sourceDir)
		if got != "4.5.6" {
			t.Errorf("expected 4.5.6, got %s", got)
		}
	})

	t.Run("config.json without version field returns unknown", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("ZEPTOCLAW_DIR", dir)
		t.Setenv("ZEPTOCLAW_BIN", "/nonexistent/zeptoclaw")

		if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"name":"test"}`), 0o644); err != nil {
			t.Fatal(err)
		}

		got := detectArchVersion("")
		if got != "unknown" {
			t.Errorf("expected unknown, got %s", got)
		}
	})
}
