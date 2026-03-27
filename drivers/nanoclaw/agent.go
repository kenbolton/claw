// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var outputStartSentinel = "---NANOCLAW_OUTPUT_START---"
var outputEndSentinel = "---NANOCLAW_OUTPUT_END---"
var npmNoticeRe = regexp.MustCompile(`^npm notice`)

func handleAgent(msg map[string]interface{}) {
	sourceDir, _ := msg["source_dir"].(string)
	groupName, _ := msg["group"].(string)
	jid, _ := msg["jid"].(string)
	prompt, _ := msg["prompt"].(string)
	sessionID, _ := msg["session_id"].(string)
	resumeAt, _ := msg["resume_at"].(string)
	native, _ := msg["native"].(bool)

	if prompt == "" {
		writeError("MISSING_PROMPT", "prompt is required")
		return
	}

	group, sourceDir, err := resolveGroup(sourceDir, groupName, jid)
	if err != nil {
		writeError("GROUP_NOT_FOUND", err.Error())
		return
	}

	secrets := readSecrets(sourceDir)

	if native {
		handleAgentNative(group, sourceDir, prompt, sessionID, resumeAt, secrets)
		return
	}

	runtime := detectRuntime()
	if runtime == "" {
		writeError("NO_RUNTIME", "neither 'container' nor 'docker' found")
		return
	}

	// Build container payload (matches Python run_container payload)
	payload := map[string]interface{}{
		"prompt":  prompt,
		"chatJid": group.JID,
		"isMain":  group.IsMain,
		"secrets": secrets,
	}
	if group.Folder != "" {
		payload["groupFolder"] = group.Folder
	}
	if sessionID != "" {
		payload["sessionId"] = sessionID
		if resumeAt == "" {
			resumeAt = "latest"
		}
		payload["resumeAt"] = resumeAt
	}

	image := "nanoclaw-agent:latest"
	args := []string{"run", "-i", "--rm"}

	addMount := func(src, dst string, ro bool) {
		if runtime == "container" {
			spec := fmt.Sprintf("type=virtiofs,source=%s,destination=%s", src, dst)
			if ro {
				spec += ",readonly"
			}
			args = append(args, "--mount", spec)
		} else {
			spec := fmt.Sprintf("%s:%s", src, dst)
			if ro {
				spec += ":ro"
			}
			args = append(args, "-v", spec)
		}
	}

	// /workspace/group — group-specific files: CLAUDE.md, memory, conversations
	groupDir := filepath.Join(sourceDir, "groups", group.Folder)
	if err := os.MkdirAll(groupDir, 0755); err == nil {
		addMount(groupDir, "/workspace/group", false)
	}

	// /workspace/project — nanoclaw source (read-only): DB, registered_groups, etc.
	addMount(sourceDir, "/workspace/project", true)

	// /home/node/.claude — session persistence across REPL turns
	sessionsDir := filepath.Join(sourceDir, "data", "sessions", group.Folder, ".claude")
	if err := os.MkdirAll(sessionsDir, 0755); err == nil {
		addMount(sessionsDir, "/home/node/.claude", false)
	}

	// additionalMounts from ContainerConfig
	if group.ContainerConfig != nil {
		var cc struct {
			AdditionalMounts []struct {
				HostPath      string `json:"hostPath"`
				ContainerPath string `json:"containerPath"`
				Readonly      bool   `json:"readonly"`
			} `json:"additionalMounts"`
		}
		if json.Unmarshal([]byte(*group.ContainerConfig), &cc) == nil {
			for _, m := range cc.AdditionalMounts {
				dst := m.ContainerPath
				if !filepath.IsAbs(dst) {
					dst = "/workspace/extra/" + dst
				}
				if err := os.MkdirAll(m.HostPath, 0755); err == nil {
					addMount(m.HostPath, dst, m.Readonly)
				}
			}
		}
	}

	args = append(args, image)
	cmd := exec.Command(runtime, args...)
	cmd.Stderr = os.Stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		writeError("SPAWN_ERROR", fmt.Sprintf("stdin pipe: %v", err))
		return
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		writeError("SPAWN_ERROR", fmt.Sprintf("stdout pipe: %v", err))
		return
	}

	if err := cmd.Start(); err != nil {
		writeError("SPAWN_ERROR", fmt.Sprintf("failed to start container: %v", err))
		return
	}

	// Write payload and close stdin
	payloadJSON, _ := json.Marshal(payload)
	_, _ = stdin.Write(payloadJSON)
	_ = stdin.Close()

	// Read container stdout line by line
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)

	inOutput := false
	var outputLines []string

	for scanner.Scan() {
		line := scanner.Text()

		// Drop npm notices
		if npmNoticeRe.MatchString(line) {
			continue
		}

		if strings.TrimSpace(line) == outputStartSentinel {
			inOutput = true
			continue
		}
		if strings.TrimSpace(line) == outputEndSentinel {
			inOutput = false
			// Parse the output JSON
			raw := strings.Join(outputLines, "\n")
			var result map[string]interface{}
			if err := json.Unmarshal([]byte(raw), &result); err != nil {
				// Not valid JSON — emit as raw text
				write(map[string]interface{}{
					"type":  "agent_output",
					"text":  raw,
					"chunk": false,
				})
			} else {
				status, _ := result["status"].(string)
				resultText, _ := result["result"].(string)
				newSessionID, _ := result["newSessionId"].(string)
				if newSessionID == "" {
					newSessionID, _ = result["sessionId"].(string)
				}

				if status == "success" {
					write(map[string]interface{}{
						"type":  "agent_output",
						"text":  resultText,
						"chunk": false,
					})
				}

				inputTokens, _ := result["inputTokens"].(float64)
				outputTokens, _ := result["outputTokens"].(float64)

				write(map[string]interface{}{
					"type":          "agent_complete",
					"session_id":    newSessionID,
					"status":        status,
					"input_tokens":  int(inputTokens),
					"output_tokens": int(outputTokens),
				})
			}

			// Kill the container — Node.js event loop may keep it alive
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return
		}

		if inOutput {
			outputLines = append(outputLines, line)
		} else {
			// Stream non-sentinel output as chunks
			write(map[string]interface{}{
				"type":  "agent_output",
				"text":  line,
				"chunk": true,
			})
		}
	}

	// No sentinel found — container exited without structured output
	rc := 0
	if err := cmd.Wait(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			rc = exitErr.ExitCode()
		}
	}

	write(map[string]interface{}{
		"type":    "agent_complete",
		"status":  "error",
		"message": fmt.Sprintf("container exited (rc=%d) without output sentinel", rc),
	})
}

// handleAgentNative runs the agent-runner directly via Node.js (no container).
// Sets up a temp workspace dir with symlinks mirroring the container layout,
// injects secrets as env vars, and passes NANOCLAW_WORKSPACE_ROOT so the
// agent-runner finds group/project/extra at the right paths.
func handleAgentNative(group *GroupRow, sourceDir, prompt, sessionID, resumeAt string, secrets map[string]string) {
	// Locate node
	node, err := exec.LookPath("node")
	if err != nil {
		writeError("NATIVE_NO_NODE", "node not found in PATH")
		return
	}

	// Locate agent-runner dist
	distIndex := filepath.Join(sourceDir, "container", "agent-runner", "dist", "index.js")
	if _, err := os.Stat(distIndex); err != nil {
		writeError("NATIVE_NO_DIST", fmt.Sprintf("agent-runner dist not found at %s — run `npm run build` in container/agent-runner/", distIndex))
		return
	}

	// Set up temp workspace: /tmp/nanoclaw-native-<folder>/
	// Intentionally persistent across calls — symlinks are rebuilt each time,
	// so reuse is safe and avoids recreating the directory structure on every prompt.
	workspaceRoot := filepath.Join(os.TempDir(), "nanoclaw-native-"+group.Folder)
	_ = os.MkdirAll(workspaceRoot, 0755)

	// group → sourceDir/groups/<folder>
	groupSrc := filepath.Join(sourceDir, "groups", group.Folder)
	_ = os.MkdirAll(groupSrc, 0755)
	groupLink := filepath.Join(workspaceRoot, "group")
	_ = os.Remove(groupLink)
	_ = os.Symlink(groupSrc, groupLink)

	// project → sourceDir (read-only semantics; symlink gives rw but driver intent is ro)
	projectLink := filepath.Join(workspaceRoot, "project")
	_ = os.Remove(projectLink)
	_ = os.Symlink(sourceDir, projectLink)

	// ipc/input dir (agent-runner polls this)
	ipcInputDir := filepath.Join(workspaceRoot, "ipc", "input")
	_ = os.MkdirAll(ipcInputDir, 0755)

	// additionalMounts → extra/<containerPath>
	extraBase := filepath.Join(workspaceRoot, "extra")
	_ = os.MkdirAll(extraBase, 0755)
	if group.ContainerConfig != nil {
		var cc struct {
			AdditionalMounts []struct {
				HostPath      string `json:"hostPath"`
				ContainerPath string `json:"containerPath"`
			} `json:"additionalMounts"`
		}
		if json.Unmarshal([]byte(*group.ContainerConfig), &cc) == nil {
			for _, m := range cc.AdditionalMounts {
				dst := m.ContainerPath
				if filepath.IsAbs(dst) {
					dst = filepath.Base(dst)
				}
				link := filepath.Join(extraBase, dst)
				_ = os.MkdirAll(filepath.Dir(link), 0755)
				_ = os.Remove(link)
				_ = os.Symlink(m.HostPath, link)
			}
		}
	}

	// Build payload
	payload := map[string]interface{}{
		"prompt":      prompt,
		"chatJid":     group.JID,
		"isMain":      group.IsMain,
		"secrets":     secrets,
		"groupFolder": group.Folder,
	}
	if sessionID != "" {
		payload["sessionId"] = sessionID
		if resumeAt == "" {
			resumeAt = "latest"
		}
		payload["resumeAt"] = resumeAt
	}
	payloadJSON, _ := json.Marshal(payload)

	// Build env: inherit host env + secrets + workspace root
	env := os.Environ()
	env = append(env, "NANOCLAW_WORKSPACE_ROOT="+workspaceRoot)
	for k, v := range secrets {
		env = append(env, k+"="+v)
	}

	cmd := exec.Command(node, distIndex)
	cmd.Env = env
	cmd.Stderr = os.Stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		writeError("SPAWN_ERROR", fmt.Sprintf("stdin pipe: %v", err))
		return
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		writeError("SPAWN_ERROR", fmt.Sprintf("stdout pipe: %v", err))
		return
	}
	if err := cmd.Start(); err != nil {
		writeError("SPAWN_ERROR", fmt.Sprintf("failed to start node: %v", err))
		return
	}

	_, _ = stdin.Write(payloadJSON)
	_ = stdin.Close()

	// Reuse the same sentinel-based output parsing as the container path
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)

	inOutput := false
	var outputLines []string

	for scanner.Scan() {
		line := scanner.Text()
		if npmNoticeRe.MatchString(line) {
			continue
		}
		if strings.TrimSpace(line) == outputStartSentinel {
			inOutput = true
			continue
		}
		if strings.TrimSpace(line) == outputEndSentinel {
			inOutput = false
			raw := strings.Join(outputLines, "\n")
			var result map[string]interface{}
			if err := json.Unmarshal([]byte(raw), &result); err != nil {
				write(map[string]interface{}{"type": "agent_output", "text": raw, "chunk": false})
			} else {
				status, _ := result["status"].(string)
				resultText, _ := result["result"].(string)
				newSessionID, _ := result["newSessionId"].(string)
				if newSessionID == "" {
					newSessionID, _ = result["sessionId"].(string)
				}
				if status == "success" {
					write(map[string]interface{}{"type": "agent_output", "text": resultText, "chunk": false})
				}
				inputTokens, _ := result["inputTokens"].(float64)
				outputTokens, _ := result["outputTokens"].(float64)
				write(map[string]interface{}{
					"type":          "agent_complete",
					"session_id":    newSessionID,
					"status":        status,
					"input_tokens":  int(inputTokens),
					"output_tokens": int(outputTokens),
				})
			}
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return
		}
		if inOutput {
			outputLines = append(outputLines, line)
		} else {
			write(map[string]interface{}{"type": "agent_output", "text": line, "chunk": true})
		}
	}

	rc := 0
	if err := cmd.Wait(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			rc = exitErr.ExitCode()
		}
	}
	write(map[string]interface{}{
		"type":    "agent_complete",
		"status":  "error",
		"message": fmt.Sprintf("node exited (rc=%d) without output sentinel", rc),
	})
}
