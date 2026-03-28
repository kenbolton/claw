// SPDX-License-Identifier: AGPL-3.0-or-later
package api

import (
	"encoding/json"
	"net/http"

	"github.com/coder/websocket"
)

func (s *Server) handleLogsWS(w http.ResponseWriter, r *http.Request) {
	group := r.PathValue("group")
	arch := r.URL.Query().Get("arch")
	if arch == "" {
		arch = "nanoclaw"
	}

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

	req := map[string]interface{}{
		"type":       "logs_request",
		"source_dir": s.SourceDir,
		"group":      group,
	}

	scanner, stdin, wait, err := d.StreamRequest(req)
	if err != nil {
		writeWSError(conn, "DRIVER_ERROR", err.Error())
		return
	}
	defer func() {
		_ = stdin.Close()
		_ = wait()
	}()

	ctx := conn.CloseRead(r.Context())

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
		}

		var msg map[string]interface{}
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}

		msgType, _ := msg["type"].(string)
		if msgType == "error" {
			writeWSError(conn, msgStr(msg, "code"), msgStr(msg, "message"))
			return
		}

		if err := conn.Write(ctx, websocket.MessageText, scanner.Bytes()); err != nil {
			return
		}
	}
}
