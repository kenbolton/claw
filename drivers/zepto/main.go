// SPDX-License-Identifier: AGPL-3.0-or-later
// claw-driver-zepto — claw driver for ZeptoClaw installations.
// Communicates via newline-delimited JSON on stdin/stdout.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

const (
	arch          = "zepto"
	driverVersion = "0.1.0"
	clawProtocol  = "0.1.0"
)

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024*1024), 200*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var msg map[string]interface{}
		if err := json.Unmarshal(line, &msg); err != nil {
			writeError("PARSE_ERROR", fmt.Sprintf("invalid JSON: %v", err))
			continue
		}
		msgType, _ := msg["type"].(string)
		switch msgType {
		case "version_request":
			handleVersion(msg)
		case "probe_request":
			sourceDir, _ := msg["source_dir"].(string)
			handleProbe(sourceDir)
		case "ps_request":
			sourceDir, _ := msg["source_dir"].(string)
			handlePs(sourceDir)
		case "agent_request":
			handleAgent(msg)
		case "watch_request":
			handleWatch(msg)
		default:
			writeError("UNKNOWN_TYPE", fmt.Sprintf("unknown message type: %q", msgType))
		}
	}
}

func handleVersion(req map[string]interface{}) {
	sourceDir, _ := req["source_dir"].(string)
	write(map[string]interface{}{
		"type":            "version_response",
		"arch":            arch,
		"arch_version":    detectArchVersion(sourceDir),
		"driver_version":  driverVersion,
		"claw_protocol":   clawProtocol,
		"driver_type":     "local",
		"requires_config": []string{},
	})
}

func handleProbe(sourceDir string) {
	confidence := probeZeptoClaw(sourceDir)
	write(map[string]interface{}{
		"type":       "probe_response",
		"arch":       arch,
		"confidence": confidence,
	})
}

// write emits one ndjson line to stdout.
func write(v interface{}) {
	data, _ := json.Marshal(v)
	fmt.Println(string(data))
}

// writeError emits an error message.
func writeError(code, message string) {
	write(map[string]string{"type": "error", "code": code, "message": message})
}
