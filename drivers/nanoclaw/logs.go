// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// handleLogs streams container stderr for a group.
// Finds the running container matching the group folder, tails its logs,
// and emits log_line messages until stdin closes.
func handleLogs(msg map[string]interface{}) {
	sourceDir, _ := msg["source_dir"].(string)
	groupName, _ := msg["group"].(string)
	if groupName == "" {
		writeError("MISSING_GROUP", "group is required for logs_request")
		return
	}

	group, _, err := resolveGroup(sourceDir, groupName, "")
	if err != nil {
		writeError("GROUP_NOT_FOUND", err.Error())
		return
	}

	runtime := detectRuntime()
	if runtime == "" {
		writeError("NO_RUNTIME", "no container runtime (docker/container) found")
		return
	}

	// Find the running container for this group
	containerName := findGroupContainer(runtime, group.Folder)
	if containerName == "" {
		writeError("NO_CONTAINER", fmt.Sprintf("no running container found for group %q", group.Folder))
		return
	}

	// Tail container logs (stderr)
	var cmd *exec.Cmd
	if runtime == "container" {
		cmd = exec.Command("container", "logs", "--follow", "-n", "100", containerName)
	} else {
		cmd = exec.Command("docker", "logs", "-f", "--tail", "100", containerName)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		writeError("SPAWN_ERROR", fmt.Sprintf("failed to pipe logs: %v", err))
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		writeError("SPAWN_ERROR", fmt.Sprintf("failed to pipe logs: %v", err))
		return
	}

	if err := cmd.Start(); err != nil {
		writeError("SPAWN_ERROR", fmt.Sprintf("failed to start log tail: %v", err))
		return
	}

	// Stream both stdout and stderr as log lines
	done := make(chan struct{})

	// Monitor stdin close to stop
	go func() {
		buf := make([]byte, 1)
		for {
			if _, err := os.Stdin.Read(buf); err != nil {
				break
			}
		}
		_ = cmd.Process.Kill()
		close(done)
	}()

	// Stream stderr (agent diagnostics)
	go func() {
		scanner := bufio.NewScanner(stderr)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			write(map[string]interface{}{
				"type":      "log_line",
				"text":      scanner.Text(),
				"timestamp": time.Now().UTC().Format(time.RFC3339),
				"stream":    "stderr",
			})
		}
	}()

	// Stream stdout (agent output)
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		select {
		case <-done:
			return
		default:
		}
		write(map[string]interface{}{
			"type":      "log_line",
			"text":      scanner.Text(),
			"timestamp": time.Now().UTC().Format(time.RFC3339),
			"stream":    "stdout",
		})
	}

	_ = cmd.Wait()
}

// findGroupContainer returns the container name/ID for a group, or "" if not running.
func findGroupContainer(runtime, folder string) string {
	if runtime == "container" {
		return findAppleContainer(folder)
	}

	// Docker: match by container name prefix
	containers := fetchContainers(runtime)
	prefix := "nanoclaw-" + folder
	for _, c := range containers {
		if c.id == prefix || strings.HasPrefix(c.id, prefix+"-") {
			return c.id
		}
	}
	return ""
}

// findAppleContainer matches a group folder to a running Apple Container
// by inspecting mount paths (groups/<folder> → /workspace/group).
func findAppleContainer(folder string) string {
	out, err := exec.Command("container", "list", "--format", "json").Output()
	if err != nil {
		return ""
	}

	var containers []struct {
		Configuration struct {
			ID    string `json:"id"`
			Image struct {
				Reference string `json:"reference"`
			} `json:"image"`
			Mounts []struct {
				Source      string `json:"source"`
				Destination string `json:"destination"`
			} `json:"mounts"`
		} `json:"configuration"`
		Status string `json:"status"`
	}
	if json.Unmarshal(out, &containers) != nil {
		return ""
	}

	suffix := "/groups/" + folder
	for _, c := range containers {
		if c.Status != "running" {
			continue
		}
		if !strings.Contains(c.Configuration.Image.Reference, "nanoclaw-agent") {
			continue
		}
		for _, m := range c.Configuration.Mounts {
			if m.Destination == "/workspace/group" && strings.HasSuffix(m.Source, suffix) {
				return c.Configuration.ID
			}
		}
	}
	return ""
}
