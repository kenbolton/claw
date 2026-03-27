// SPDX-License-Identifier: AGPL-3.0-or-later
package api

import (
	"net/http"

	"github.com/kenbolton/claw/driver"
)

func (s *Server) handlePs(w http.ResponseWriter, r *http.Request) {
	arch := r.URL.Query().Get("arch")
	drivers := s.filterDrivers(arch)
	if len(drivers) == 0 {
		writeError(w, http.StatusNotFound, "ARCH_NOT_FOUND", "no driver found for arch")
		return
	}

	results := fanOut(drivers, func(d *driver.Driver) map[string]interface{} {
		return map[string]interface{}{
			"type":       "ps_request",
			"source_dir": s.SourceDir,
		}
	})

	type instance struct {
		ID     string `json:"id"`
		Arch   string `json:"arch"`
		Group  string `json:"group"`
		Folder string `json:"folder"`
		JID    string `json:"jid"`
		State  string `json:"state"`
		Age    string `json:"age"`
		IsMain bool   `json:"is_main"`
	}

	var instances []instance
	allFailed := true

	for _, res := range results {
		if res.Err != nil {
			continue
		}
		allFailed = false
		for _, msg := range res.Messages {
			msgType, _ := msg["type"].(string)
			if msgType == "instance" {
				inst := instance{
					ID:     msgStr(msg, "id"),
					Arch:   msgStr(msg, "arch"),
					Group:  msgStr(msg, "group"),
					Folder: msgStr(msg, "folder"),
					JID:    msgStr(msg, "jid"),
					State:  msgStr(msg, "state"),
					Age:    msgStr(msg, "age"),
				}
				if v, ok := msg["is_main"].(bool); ok {
					inst.IsMain = v
				}
				instances = append(instances, inst)
			}
		}
	}

	if allFailed && len(results) > 0 {
		writeError(w, http.StatusBadGateway, "DRIVER_ERROR", "all drivers failed")
		return
	}

	if instances == nil {
		instances = []instance{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"instances": instances,
	})
}

// msgStr extracts a string from a map.
func msgStr(m map[string]interface{}, key string) string {
	v, _ := m[key].(string)
	return v
}
