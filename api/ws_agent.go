// SPDX-License-Identifier: AGPL-3.0-or-later
package api

import (
	"encoding/json"
	"net/http"

	"github.com/coder/websocket"
)

func (s *Server) handleAgentWS(w http.ResponseWriter, r *http.Request) {
	group := r.PathValue("group")
	arch := r.URL.Query().Get("arch")
	if arch == "" {
		arch = "nanoclaw"
	}
	sessionID := r.URL.Query().Get("session")
	native := r.URL.Query().Get("native") == "true"

	d := s.locateDriver(arch)
	if d == nil {
		writeError(w, http.StatusNotFound, "ARCH_NOT_FOUND", "no driver found for arch")
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: s.wsOriginPatterns(),
	})
	if err != nil {
		return
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()

	ctx := r.Context()

	for {
		// Read prompt from client.
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}

		var clientMsg struct {
			Prompt string `json:"prompt"`
		}
		if err := json.Unmarshal(data, &clientMsg); err != nil || clientMsg.Prompt == "" {
			writeWSError(conn, "MISSING_PROMPT", "client message must include a prompt field")
			return
		}

		// Build and send agent request.
		req := map[string]interface{}{
			"type":       "agent_request",
			"source_dir": s.SourceDir,
			"group":      group,
			"jid":        "",
			"prompt":     clientMsg.Prompt,
			"session_id": sessionID,
			"resume_at":  "",
			"native":     native,
			"verbose":    false,
		}

		scanner, wait, err := d.SendRequestAndClose(req)
		if err != nil {
			writeWSError(conn, "DRIVER_ERROR", err.Error())
			return
		}

		// Stream driver output to WebSocket.
		done := false
		for scanner.Scan() {
			var msg map[string]interface{}
			if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
				continue
			}

			msgType, _ := msg["type"].(string)
			if msgType == "error" {
				writeWSError(conn, msgStr(msg, "code"), msgStr(msg, "message"))
				_ = wait()
				return
			}

			if err := conn.Write(ctx, websocket.MessageText, scanner.Bytes()); err != nil {
				_ = wait()
				return
			}

			if msgType == "agent_complete" {
				// Thread session ID for multi-turn.
				if sid, ok := msg["session_id"].(string); ok && sid != "" {
					sessionID = sid
				}
				done = true
			}
		}
		_ = wait()

		if !done {
			writeWSError(conn, "DRIVER_ERROR", "driver exited without agent_complete")
			return
		}

		// Wait for next prompt or disconnect.
		// Use a short peek to check if context is cancelled.
		select {
		case <-ctx.Done():
			return
		default:
		}
	}
}
