// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
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
	if group.Name != "" {
		payload["groupFolder"] = group.Name
	}
	if sessionID != "" {
		payload["sessionId"] = sessionID
		if resumeAt == "" {
			resumeAt = "latest"
		}
		payload["resumeAt"] = resumeAt
	}

	image := "nanoclaw-agent:latest"
	cmd := exec.Command(runtime, "run", "-i", "--rm", image)
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
