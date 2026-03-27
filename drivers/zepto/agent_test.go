// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestEmitAgentResponse(t *testing.T) {
	t.Run("success with content", func(t *testing.T) {
		raw := `{"request_id":"req-1","result":{"content":"Hello world"},"usage":{"input_tokens":10,"output_tokens":5,"tool_calls":0,"errors":0}}`

		lines := captureEmitAgentResponse(t, raw, "")
		if len(lines) < 2 {
			t.Fatalf("expected at least 2 lines, got %d", len(lines))
		}

		// First line: agent_output with content
		if lines[0]["type"] != "agent_output" {
			t.Errorf("expected type=agent_output, got %q", lines[0]["type"])
		}
		if lines[0]["text"] != "Hello world" {
			t.Errorf("expected text='Hello world', got %q", lines[0]["text"])
		}
		if lines[0]["chunk"] != false {
			t.Errorf("expected chunk=false")
		}

		// Second line: agent_complete
		if lines[1]["type"] != "agent_complete" {
			t.Errorf("expected type=agent_complete, got %q", lines[1]["type"])
		}
		if lines[1]["status"] != "success" {
			t.Errorf("expected status=success, got %q", lines[1]["status"])
		}
		// Check usage
		if lines[1]["input_tokens"] != float64(10) {
			t.Errorf("expected input_tokens=10, got %v", lines[1]["input_tokens"])
		}
		if lines[1]["output_tokens"] != float64(5) {
			t.Errorf("expected output_tokens=5, got %v", lines[1]["output_tokens"])
		}
	})

	t.Run("success without usage", func(t *testing.T) {
		raw := `{"request_id":"req-2","result":{"content":"No usage info"}}`
		lines := captureEmitAgentResponse(t, raw, "")
		if len(lines) < 2 {
			t.Fatalf("expected at least 2 lines, got %d", len(lines))
		}

		if lines[1]["status"] != "success" {
			t.Errorf("expected status=success")
		}
		// No usage fields
		if _, ok := lines[1]["input_tokens"]; ok {
			t.Errorf("expected no input_tokens field")
		}
	})

	t.Run("error result", func(t *testing.T) {
		raw := `{"request_id":"req-3","result":{"message":"something broke","code":"RATE_LIMIT"}}`
		lines := captureEmitAgentResponse(t, raw, "")
		if len(lines) < 2 {
			t.Fatalf("expected at least 2 lines, got %d", len(lines))
		}

		// First line should be error
		if lines[0]["type"] != "error" {
			t.Errorf("expected type=error, got %q", lines[0]["type"])
		}
		if lines[0]["code"] != "RATE_LIMIT" {
			t.Errorf("expected code=RATE_LIMIT, got %q", lines[0]["code"])
		}
		if lines[0]["message"] != "something broke" {
			t.Errorf("expected message='something broke', got %q", lines[0]["message"])
		}

		// Second line: agent_complete with error status
		if lines[1]["status"] != "error" {
			t.Errorf("expected status=error")
		}
	})

	t.Run("error result with default code", func(t *testing.T) {
		raw := `{"request_id":"req-4","result":{"message":"unknown error"}}`
		lines := captureEmitAgentResponse(t, raw, "")
		if len(lines) < 2 {
			t.Fatalf("expected at least 2 lines, got %d", len(lines))
		}

		if lines[0]["code"] != "AGENT_ERROR" {
			t.Errorf("expected code=AGENT_ERROR, got %q", lines[0]["code"])
		}
	})

	t.Run("unrecognised response", func(t *testing.T) {
		raw := `{"request_id":"req-5","result":{}}`
		lines := captureEmitAgentResponse(t, raw, "")
		if len(lines) < 2 {
			t.Fatalf("expected at least 2 lines, got %d", len(lines))
		}

		if lines[0]["type"] != "error" {
			t.Errorf("expected type=error, got %q", lines[0]["type"])
		}
		if lines[0]["code"] != "UNKNOWN_RESPONSE" {
			t.Errorf("expected code=UNKNOWN_RESPONSE, got %q", lines[0]["code"])
		}
	})

	t.Run("non-JSON raw text", func(t *testing.T) {
		raw := "Just some plain text output"
		lines := captureEmitAgentResponse(t, raw, "")
		if len(lines) < 2 {
			t.Fatalf("expected at least 2 lines, got %d", len(lines))
		}

		if lines[0]["type"] != "agent_output" {
			t.Errorf("expected type=agent_output, got %q", lines[0]["type"])
		}
		if lines[0]["text"] != "Just some plain text output" {
			t.Errorf("expected raw text, got %q", lines[0]["text"])
		}
		if lines[1]["status"] != "success" {
			t.Errorf("expected status=success for raw text")
		}
	})

	t.Run("session ID passthrough", func(t *testing.T) {
		raw := `{"request_id":"req-6","result":{"content":"ok"}}`
		lines := captureEmitAgentResponse(t, raw, "cli:session-abc")
		if len(lines) < 2 {
			t.Fatalf("expected at least 2 lines, got %d", len(lines))
		}

		if lines[1]["session_id"] != "cli:session-abc" {
			t.Errorf("expected session_id='cli:session-abc', got %q", lines[1]["session_id"])
		}
	})

	t.Run("session key from response string", func(t *testing.T) {
		raw := `{"request_id":"req-7","result":{"content":"ok","session":"cli:returned-key"}}`
		lines := captureEmitAgentResponse(t, raw, "cli:original")
		if len(lines) < 2 {
			t.Fatalf("expected at least 2 lines, got %d", len(lines))
		}

		if lines[1]["session_id"] != "cli:returned-key" {
			t.Errorf("expected session_id='cli:returned-key', got %q", lines[1]["session_id"])
		}
	})

	t.Run("session key from response map", func(t *testing.T) {
		raw := `{"request_id":"req-8","result":{"content":"ok","session":{"key":"cli:map-key","other":"data"}}}`
		lines := captureEmitAgentResponse(t, raw, "cli:original")
		if len(lines) < 2 {
			t.Fatalf("expected at least 2 lines, got %d", len(lines))
		}

		if lines[1]["session_id"] != "cli:map-key" {
			t.Errorf("expected session_id='cli:map-key', got %q", lines[1]["session_id"])
		}
	})
}

func TestHandleAgentMissingPrompt(t *testing.T) {
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	handleAgent(map[string]interface{}{
		"type": "agent_request",
	})

	_ = w.Close()
	os.Stdout = oldStdout

	var buf [8192]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])

	lines := parseNDJSONLines(t, output)
	if len(lines) == 0 {
		t.Fatal("expected error output")
	}
	if lines[0]["type"] != "error" {
		t.Errorf("expected type=error, got %q", lines[0]["type"])
	}
	if lines[0]["code"] != "MISSING_PROMPT" {
		t.Errorf("expected code=MISSING_PROMPT, got %q", lines[0]["code"])
	}
}

func TestHandleAgentSpawnError(t *testing.T) {
	// Use a non-existent binary to trigger SPAWN_ERROR
	t.Setenv("ZEPTOCLAW_BIN", "/nonexistent/zeptoclaw-binary-that-does-not-exist")

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	handleAgent(map[string]interface{}{
		"type":   "agent_request",
		"prompt": "hello",
	})

	_ = w.Close()
	os.Stdout = oldStdout

	var buf [8192]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])

	lines := parseNDJSONLines(t, output)
	if len(lines) == 0 {
		t.Fatal("expected error output")
	}
	if lines[0]["type"] != "error" {
		t.Errorf("expected type=error, got %q", lines[0]["type"])
	}
	if lines[0]["code"] != "SPAWN_ERROR" {
		t.Errorf("expected code=SPAWN_ERROR, got %q", lines[0]["code"])
	}
}

func TestResponseMarkerConstants(t *testing.T) {
	if responseStartMarker != "<<<AGENT_RESPONSE_START>>>" {
		t.Errorf("unexpected start marker: %q", responseStartMarker)
	}
	if responseEndMarker != "<<<AGENT_RESPONSE_END>>>" {
		t.Errorf("unexpected end marker: %q", responseEndMarker)
	}
}

// captureEmitAgentResponse captures the NDJSON output from emitAgentResponse.
func captureEmitAgentResponse(t *testing.T, raw, sessionID string) []map[string]interface{} {
	t.Helper()
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w

	emitAgentResponse(raw, sessionID)

	_ = w.Close()
	os.Stdout = oldStdout

	var buf [16384]byte
	n, _ := r.Read(buf[:])
	output := string(buf[:n])
	_ = r.Close()

	return parseNDJSONLines(t, output)
}

func parseNDJSONLines(t *testing.T, output string) []map[string]interface{} {
	t.Helper()
	var lines []map[string]interface{}
	for _, raw := range strings.Split(output, "\n") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		var msg map[string]interface{}
		if err := json.Unmarshal([]byte(raw), &msg); err != nil {
			t.Logf("skipping non-JSON line: %s (err: %v)", raw, err)
			continue
		}
		lines = append(lines, msg)
	}
	if len(lines) == 0 {
		t.Logf("full output: %s", output)
	}
	return lines
}

func TestAgentRequestEncoding(t *testing.T) {
	// Verify the request JSON structure is valid by checking the encoding path.
	// This tests the map construction without spawning a process.
	sessionKey := "cli:test-session"
	request := map[string]interface{}{
		"request_id": fmt.Sprintf("claw-%d", 12345),
		"message": map[string]interface{}{
			"channel":     "cli",
			"sender_id":   "user",
			"chat_id":     "cli",
			"content":     "test prompt",
			"media":       []interface{}{},
			"session_key": sessionKey,
			"metadata":    map[string]interface{}{},
		},
		"agent_config": map[string]interface{}{},
		"session":      nil,
	}
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("failed to encode request: %v", err)
	}

	// Verify it round-trips
	var decoded map[string]interface{}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	msg, _ := decoded["message"].(map[string]interface{})
	if msg["session_key"] != "cli:test-session" {
		t.Errorf("expected session_key='cli:test-session', got %q", msg["session_key"])
	}
	if msg["content"] != "test prompt" {
		t.Errorf("expected content='test prompt', got %q", msg["content"])
	}
}
