// SPDX-License-Identifier: AGPL-3.0-or-later
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/coder/websocket"
)

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	data, _ := json.Marshal(v)
	_, _ = w.Write(data)
}

// writeError writes a JSON error response.
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{
		"error":   code,
		"message": message,
	})
}

// writeWSError writes an error message to a WebSocket and closes it.
func writeWSError(conn *websocket.Conn, code, message string) {
	data, _ := json.Marshal(map[string]string{
		"type":    "error",
		"code":    code,
		"message": message,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = conn.Write(ctx, websocket.MessageText, data)
	_ = conn.Close(websocket.StatusInternalError, message)
}

// driverCodeToStatus maps driver error codes to HTTP status codes.
func driverCodeToStatus(code string) int {
	switch code {
	case "GROUP_NOT_FOUND":
		return http.StatusNotFound
	case "UNSUPPORTED":
		return http.StatusNotImplemented
	default:
		return http.StatusBadGateway
	}
}
