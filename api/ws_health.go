// SPDX-License-Identifier: AGPL-3.0-or-later
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/coder/websocket"
	"github.com/kenbolton/claw/driver"
)

func (s *Server) handleHealthWS(w http.ResponseWriter, r *http.Request) {
	interval := 30
	if i := r.URL.Query().Get("interval"); i != "" {
		if v, err := strconv.Atoi(i); err == nil && v > 0 {
			interval = v
		}
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: s.wsOriginPatterns(),
	})
	if err != nil {
		return
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()

	ctx := conn.CloseRead(r.Context())
	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()

	// Run immediately, then on interval.
	for {
		if err := s.streamHealthRound(ctx, conn); err != nil {
			return
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// streamHealthRound runs one round of health checks and streams results to the WebSocket.
func (s *Server) streamHealthRound(ctx context.Context, conn *websocket.Conn) error {
	results := fanOut(s.Drivers, func(d *driver.Driver) map[string]interface{} {
		return map[string]interface{}{
			"type":       "health_request",
			"source_dir": s.SourceDir,
			"group":      "",
			"checks":     []string{},
		}
	})

	for _, res := range results {
		if res.Err != nil {
			continue
		}
		for _, msg := range res.Messages {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			msgType, _ := msg["type"].(string)
			// Rename check_result → check and add arch for the WS consumer.
			if msgType == "check_result" {
				msg["type"] = "check"
				msg["arch"] = res.Driver.Arch
			}
			if msgType == "health_complete" {
				msg["arch"] = res.Driver.Arch
				msg["ts"] = time.Now().UTC().Format(time.RFC3339)
			}

			data, _ := json.Marshal(msg)
			if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
				return err
			}
		}
	}
	return nil
}
