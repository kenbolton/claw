// SPDX-License-Identifier: AGPL-3.0-or-later
package cmd

import (
	"os/exec"
	"testing"

	"github.com/kenbolton/claw/driver"
)

func TestComputeExitCode(t *testing.T) {
	tests := []struct {
		name        string
		results     []checkResult
		driverError bool
		want        int
	}{
		{
			name:        "all pass",
			results:     []checkResult{{Status: "pass"}, {Status: "pass"}, {Status: "pass"}},
			driverError: false,
			want:        0,
		},
		{
			name:        "has failure",
			results:     []checkResult{{Status: "pass"}, {Status: "fail"}, {Status: "pass"}},
			driverError: false,
			want:        1,
		},
		{
			name:        "warn only",
			results:     []checkResult{{Status: "pass"}, {Status: "warn"}, {Status: "pass"}},
			driverError: false,
			want:        2,
		},
		{
			name:        "fail takes precedence over warn",
			results:     []checkResult{{Status: "warn"}, {Status: "fail"}, {Status: "pass"}},
			driverError: false,
			want:        1,
		},
		{
			name:        "driver error with no results",
			results:     nil,
			driverError: true,
			want:        3,
		},
		{
			name:        "driver error with fallback results",
			results:     []checkResult{{Status: "pass"}},
			driverError: true,
			want:        0,
		},
		{
			name:        "empty results no error",
			results:     nil,
			driverError: false,
			want:        0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeExitCode(tt.results, tt.driverError)
			if got != tt.want {
				t.Errorf("computeExitCode() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestStatusSymbol(t *testing.T) {
	tests := []struct {
		status string
		want   string
	}{
		{"pass", "\u2713"},
		{"warn", "\u26A0"},
		{"fail", "\u2717"},
		{"unknown", "?"},
	}
	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			got := statusSymbol(tt.status)
			if got != tt.want {
				t.Errorf("statusSymbol(%q) = %q, want %q", tt.status, got, tt.want)
			}
		})
	}
}

func TestCountStatuses(t *testing.T) {
	results := []checkResult{
		{Status: "pass"},
		{Status: "pass"},
		{Status: "warn"},
		{Status: "fail"},
		{Status: "pass"},
	}
	pass, warn, fail := countStatuses(results)
	if pass != 3 {
		t.Errorf("pass = %d, want 3", pass)
	}
	if warn != 1 {
		t.Errorf("warn = %d, want 1", warn)
	}
	if fail != 1 {
		t.Errorf("fail = %d, want 1", fail)
	}
}

func TestFallbackChecks(t *testing.T) {
	// Test that fallback checks produce results even without a real driver.
	// We can't test the full driver flow without a binary, but we can test
	// the fallback check functions directly.

	t.Run("runtime fallback with docker in path", func(t *testing.T) {
		// Mock lookPath.
		origLookPath := lookPath
		lookPath = func(name string) (string, error) {
			if name == "docker" {
				return "/usr/local/bin/docker", nil
			}
			return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
		}
		defer func() { lookPath = origLookPath }()

		d := &driver.Driver{Arch: "test"}
		results := fallbackChecks(d)

		var runtimeResult *checkResult
		for i := range results {
			if results[i].Name == "runtime" {
				runtimeResult = &results[i]
				break
			}
		}
		if runtimeResult == nil {
			t.Fatal("no runtime check in fallback results")
		}
		if runtimeResult.Status != "pass" {
			t.Errorf("expected pass, got %s", runtimeResult.Status)
		}
	})

	t.Run("credentials fallback from env", func(t *testing.T) {
		origLookPath := lookPath
		lookPath = func(name string) (string, error) {
			return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
		}
		defer func() { lookPath = origLookPath }()

		t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test-key")
		t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")

		d := &driver.Driver{Arch: "test"}
		results := fallbackChecks(d)

		var credResult *checkResult
		for i := range results {
			if results[i].Name == "credentials" {
				credResult = &results[i]
				break
			}
		}
		if credResult == nil {
			t.Fatal("no credentials check in fallback results")
		}
		if credResult.Status != "pass" {
			t.Errorf("expected pass, got %s: %s", credResult.Status, credResult.Detail)
		}
	})

	t.Run("no credentials", func(t *testing.T) {
		origLookPath := lookPath
		lookPath = func(name string) (string, error) {
			return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
		}
		defer func() { lookPath = origLookPath }()

		t.Setenv("ANTHROPIC_API_KEY", "")
		t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")

		d := &driver.Driver{Arch: "test"}
		results := fallbackChecks(d)

		var credResult *checkResult
		for i := range results {
			if results[i].Name == "credentials" {
				credResult = &results[i]
				break
			}
		}
		if credResult == nil {
			t.Fatal("no credentials check in fallback results")
		}
		if credResult.Status != "fail" {
			t.Errorf("expected fail, got %s", credResult.Status)
		}
	})
}
