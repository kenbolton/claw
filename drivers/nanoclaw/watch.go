// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"os"
	"time"
)

func handleWatch(msg map[string]interface{}) {
	sourceDir, _ := msg["source_dir"].(string)
	groupName, _ := msg["group"].(string)
	jid, _ := msg["jid"].(string)
	lines := 20
	if v, ok := msg["lines"].(float64); ok && v > 0 {
		lines = int(v)
	}

	group, sourceDir, err := resolveGroup(sourceDir, groupName, jid)
	if err != nil {
		writeError("GROUP_NOT_FOUND", err.Error())
		return
	}

	agentName := "Agent"

	// Emit historical messages
	history, err := readMessages(sourceDir, group.JID, lines)
	if err != nil {
		writeError("DB_ERROR", err.Error())
		return
	}

	lastTS := ""
	for _, m := range history {
		emitMessage(m, agentName)
		lastTS = m.Timestamp
	}

	if lastTS == "" {
		lastTS = time.Now().UTC().Format(time.RFC3339)
	}

	// Watch for stdin close in a goroutine
	done := make(chan struct{})
	go func() {
		buf := make([]byte, 1)
		_, _ = os.Stdin.Read(buf)
		close(done)
	}()

	// Poll loop
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			msgs, err := readNewMessages(sourceDir, group.JID, lastTS)
			if err != nil {
				continue
			}
			for _, m := range msgs {
				emitMessage(m, agentName)
				lastTS = m.Timestamp
			}
		}
	}
}

func emitMessage(m MessageRow, agentName string) {
	sender := m.SenderName
	if m.IsBotMessage {
		sender = agentName
	} else if m.IsFromMe {
		if sender == "" {
			sender = "You"
		}
	} else if sender == "" {
		sender = "?"
	}

	write(map[string]interface{}{
		"type":      "message",
		"timestamp": m.Timestamp,
		"sender":    sender,
		"content":   m.Content,
		"is_bot":    m.IsBotMessage,
	})
}
