// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

func TestHandleGroupsUnsupported(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	handleGroups(map[string]interface{}{
		"type":       "groups_request",
		"source_dir": "",
	})

	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)

	var msg map[string]interface{}
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &msg); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if msg["type"] != "error" {
		t.Errorf("expected error, got %v", msg["type"])
	}
	if msg["code"] != "UNSUPPORTED" {
		t.Errorf("expected UNSUPPORTED, got %v", msg["code"])
	}
}
