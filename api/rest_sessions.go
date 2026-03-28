// SPDX-License-Identifier: AGPL-3.0-or-later
package api

import (
	"encoding/json"
	"net/http"
	"strconv"
)

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	arch := r.URL.Query().Get("arch")
	group := r.URL.Query().Get("group")

	if arch == "" || group == "" {
		writeError(w, http.StatusBadRequest, "MISSING_PARAM", "arch and group query params are required")
		return
	}

	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 {
			limit = v
		}
	}

	d := s.locateDriver(arch)
	if d == nil {
		writeError(w, http.StatusNotFound, "ARCH_NOT_FOUND", "no driver found for arch")
		return
	}

	req := map[string]interface{}{
		"type":       "sessions_request",
		"source_dir": s.SourceDir,
		"group":      group,
		"limit":      limit,
	}

	scanner, wait, err := d.SendRequestAndClose(req)
	if err != nil {
		writeError(w, http.StatusBadGateway, "DRIVER_ERROR", err.Error())
		return
	}

	type sessionInfo struct {
		SessionID    string `json:"session_id"`
		Group        string `json:"group"`
		StartedAt    string `json:"started_at"`
		LastActive   string `json:"last_active"`
		MessageCount int    `json:"message_count"`
		Summary      string `json:"summary"`
		Resumable    bool   `json:"resumable,omitempty"`
	}

	var sessions []sessionInfo

	for scanner.Scan() {
		var msg map[string]interface{}
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}
		msgType, _ := msg["type"].(string)
		switch msgType {
		case "session":
			sess := sessionInfo{
				SessionID:  msgStr(msg, "session_id"),
				Group:      msgStr(msg, "group"),
				StartedAt:  msgStr(msg, "started_at"),
				LastActive: msgStr(msg, "last_active"),
				Summary:    msgStr(msg, "summary"),
			}
			if v, ok := msg["message_count"].(float64); ok {
				sess.MessageCount = int(v)
			}
			if v, ok := msg["resumable"].(bool); ok {
				sess.Resumable = v
			}
			sessions = append(sessions, sess)
		case "error":
			code := msgStr(msg, "code")
			message := msgStr(msg, "message")
			_ = wait()
			writeError(w, driverCodeToStatus(code), code, message)
			return
		case "sessions_complete":
			// done
		}
	}
	if err := scanner.Err(); err != nil {
		_ = wait()
		writeError(w, http.StatusBadGateway, "DRIVER_ERROR", err.Error())
		return
	}
	_ = wait()

	if sessions == nil {
		sessions = []sessionInfo{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"sessions": sessions,
	})
}
