// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ZeptoClaw stdout sentinels (from src/gateway/ipc.rs)
const (
	responseStartMarker = "<<<AGENT_RESPONSE_START>>>"
	responseEndMarker   = "<<<AGENT_RESPONSE_END>>>"
)

func handleAgent(msg map[string]interface{}) {
	prompt, _ := msg["prompt"].(string)
	if prompt == "" {
		writeError("MISSING_PROMPT", "prompt is required")
		return
	}

	sessionID, _ := msg["session_id"].(string)

	bin := findBinary()

	// Build the AgentRequest JSON matching ZeptoClaw's gateway/ipc.go types.
	// session_key format: "channel:chat_id"
	sessionKey := "cli:cli"
	if sessionID != "" {
		sessionKey = sessionID
	}

	request := map[string]interface{}{
		"request_id": fmt.Sprintf("claw-%d", os.Getpid()),
		"message": map[string]interface{}{
			"channel":     "cli",
			"sender_id":   "user",
			"chat_id":     "cli",
			"content":     prompt,
			"media":       []interface{}{},
			"session_key": sessionKey,
			"metadata":    map[string]interface{}{},
		},
		"agent_config": map[string]interface{}{},
		"session":      nil,
	}
	requestJSON, err := json.Marshal(request)
	if err != nil {
		writeError("ENCODE_ERROR", fmt.Sprintf("failed to encode request: %v", err))
		return
	}

	cmd := exec.Command(bin, "agent-stdin")
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
		writeError("SPAWN_ERROR", fmt.Sprintf("failed to start zeptoclaw: %v", err))
		return
	}

	// Write request and close stdin
	_, _ = stdin.Write(requestJSON)
	_, _ = stdin.Write([]byte("\n"))
	_ = stdin.Close()

	// Read stdout line by line
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)

	var collecting bool
	var responseLines []string

	for scanner.Scan() {
		line := scanner.Text()

		if strings.TrimSpace(line) == responseStartMarker {
			collecting = true
			continue
		}
		if strings.TrimSpace(line) == responseEndMarker {
			// Parse and emit
			raw := strings.Join(responseLines, "\n")
			emitAgentResponse(raw, sessionID)
			_ = cmd.Wait()
			return
		}

		if collecting {
			responseLines = append(responseLines, line)
		} else {
			// Stream pre-sentinel lines as chunks
			if line != "" {
				write(map[string]interface{}{
					"type":  "agent_output",
					"text":  line,
					"chunk": true,
				})
			}
		}
	}

	// No sentinel found
	rc := 0
	if err := cmd.Wait(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			rc = exitErr.ExitCode()
		}
	}
	write(map[string]interface{}{
		"type":    "agent_complete",
		"status":  "error",
		"message": fmt.Sprintf("zeptoclaw exited (rc=%d) without response markers", rc),
	})
}

// agentResponse mirrors ZeptoClaw's AgentResponse type.
type agentResponse struct {
	RequestID string      `json:"request_id"`
	Result    agentResult `json:"result"`
	Usage     *usageSnap  `json:"usage,omitempty"`
}

type agentResult struct {
	// Success fields (flat format)
	Content *string     `json:"content,omitempty"`
	Session interface{} `json:"session,omitempty"`
	// Error fields (flat format)
	Message *string `json:"message,omitempty"`
	Code    *string `json:"code,omitempty"`
	// Rust enum-style wrapper: {"Ok": {...}} or {"Error": {...}}
	Ok    *agentResultInner `json:"Ok,omitempty"`
	Error *agentResultInner `json:"Error,omitempty"`
}

type agentResultInner struct {
	Content *string     `json:"content,omitempty"`
	Session interface{} `json:"session,omitempty"`
	Message *string     `json:"message,omitempty"`
	Code    *string     `json:"code,omitempty"`
}

type usageSnap struct {
	InputTokens  uint64 `json:"input_tokens"`
	OutputTokens uint64 `json:"output_tokens"`
	ToolCalls    uint64 `json:"tool_calls"`
	Errors       uint64 `json:"errors"`
}

func emitAgentResponse(raw, sessionID string) {
	raw = strings.TrimSpace(raw)

	var resp agentResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		// Not valid JSON — emit as raw text
		write(map[string]interface{}{
			"type":  "agent_output",
			"text":  raw,
			"chunk": false,
		})
		write(map[string]interface{}{
			"type":   "agent_complete",
			"status": "success",
		})
		return
	}

	// Unwrap Rust enum-style result: {"Ok": {...}} or {"Error": {...}}
	if resp.Result.Ok != nil {
		if resp.Result.Content == nil {
			resp.Result.Content = resp.Result.Ok.Content
		}
		if resp.Result.Session == nil {
			resp.Result.Session = resp.Result.Ok.Session
		}
	}
	if resp.Result.Error != nil {
		if resp.Result.Message == nil {
			resp.Result.Message = resp.Result.Error.Message
		}
		if resp.Result.Code == nil {
			resp.Result.Code = resp.Result.Error.Code
		}
	}

	// Check result type via presence of fields
	if resp.Result.Content != nil {
		write(map[string]interface{}{
			"type":  "agent_output",
			"text":  *resp.Result.Content,
			"chunk": false,
		})
		complete := map[string]interface{}{
			"type":   "agent_complete",
			"status": "success",
		}
		// Prefer a session key returned by zepto; fall back to the one we sent
		// so the REPL can thread it through on the next turn.
		outSessionKey := sessionID
		if s, ok := resp.Result.Session.(string); ok && s != "" {
			outSessionKey = s
		} else if m, ok := resp.Result.Session.(map[string]interface{}); ok {
			if key, ok := m["key"].(string); ok && key != "" {
				outSessionKey = key
			}
		}
		if outSessionKey != "" {
			complete["session_id"] = outSessionKey
		}
		if resp.Usage != nil {
			complete["input_tokens"] = resp.Usage.InputTokens
			complete["output_tokens"] = resp.Usage.OutputTokens
		}
		write(complete)
	} else if resp.Result.Message != nil {
		msg := *resp.Result.Message
		code := "AGENT_ERROR"
		if resp.Result.Code != nil {
			code = *resp.Result.Code
		}
		writeError(code, msg)
		write(map[string]interface{}{
			"type":   "agent_complete",
			"status": "error",
		})
	} else {
		writeError("UNKNOWN_RESPONSE", fmt.Sprintf("unrecognised response: %s", raw))
		write(map[string]interface{}{
			"type":   "agent_complete",
			"status": "error",
		})
	}
}
