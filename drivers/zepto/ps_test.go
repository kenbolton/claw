// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"encoding/json"
	"os"
	"testing"
)

func TestHumanAge(t *testing.T) {
	tests := []struct {
		secs int64
		want string
	}{
		{0, "0s"},
		{-5, "0s"},
		{30, "30s"},
		{59, "59s"},
		{60, "1m 00s"},
		{90, "1m 30s"},
		{3599, "59m 59s"},
		{3600, "1h 00m"},
		{3661, "1h 01m"},
		{86399, "23h 59m"},
		{86400, "1d 00h"},
		{90061, "1d 01h"},
		{172800, "2d 00h"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := humanAge(tt.secs)
			if got != tt.want {
				t.Errorf("humanAge(%d) = %q, want %q", tt.secs, got, tt.want)
			}
		})
	}
}

func TestNativeInstancesFiltering(t *testing.T) {
	// Smoke test: nativeInstances should return valid instance maps or nil.
	instances := nativeInstances()
	for _, inst := range instances {
		mode, _ := inst["mode"].(string)
		if mode != "gateway" && mode != "daemon" {
			t.Errorf("unexpected mode %q, want gateway or daemon", mode)
		}
		if inst["type"] != "instance" {
			t.Errorf("unexpected type %q, want instance", inst["type"])
		}
		if inst["arch"] != arch {
			t.Errorf("unexpected arch %q, want %s", inst["arch"], arch)
		}
	}
}

func TestDockerZeptoContainersParsing(t *testing.T) {
	// Gracefully handles when docker isn't available (returns nil).
	containers := dockerZeptoContainers()
	for _, c := range containers {
		if c["type"] != "instance" {
			t.Errorf("unexpected type %q", c["type"])
		}
		if c["containerized"] != true {
			t.Errorf("expected containerized=true")
		}
	}
}

func TestHandlePsOutput(t *testing.T) {
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	handlePs("")

	_ = w.Close()
	os.Stdout = oldStdout

	var buf [16384]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])

	var lines []map[string]interface{}
	for _, line := range splitLines(output) {
		line = trimSpace(line)
		if line == "" {
			continue
		}
		var msg map[string]interface{}
		if json.Unmarshal([]byte(line), &msg) == nil {
			lines = append(lines, msg)
		}
	}

	if len(lines) == 0 {
		t.Fatal("expected at least 1 NDJSON line (ps_complete)")
	}

	// Last line should be ps_complete
	last := lines[len(lines)-1]
	if last["type"] != "ps_complete" {
		t.Errorf("expected last line type=ps_complete, got %q", last["type"])
	}

	// All non-complete lines should be instances
	for _, line := range lines[:len(lines)-1] {
		if line["type"] != "instance" {
			t.Errorf("expected type=instance, got %q", line["type"])
		}
	}
}
