// SPDX-License-Identifier: AGPL-3.0-or-later
package api

import (
	"net/http"

	"github.com/kenbolton/claw/driver"
)

func (s *Server) handleGroups(w http.ResponseWriter, r *http.Request) {
	arch := r.URL.Query().Get("arch")
	drivers := s.filterDrivers(arch)
	if len(drivers) == 0 {
		writeError(w, http.StatusNotFound, "ARCH_NOT_FOUND", "no driver found for arch")
		return
	}

	results := fanOut(drivers, func(d *driver.Driver) map[string]interface{} {
		return map[string]interface{}{
			"type":       "groups_request",
			"source_dir": s.SourceDir,
		}
	})

	type groupInfo struct {
		Arch            string `json:"arch"`
		SourceDir       string `json:"source_dir"`
		JID             string `json:"jid"`
		Name            string `json:"name"`
		Folder          string `json:"folder"`
		Trigger         string `json:"trigger"`
		IsMain          bool   `json:"is_main"`
		RequiresTrigger bool   `json:"requires_trigger"`
	}

	var groups []groupInfo

	for _, res := range results {
		if res.Err != nil {
			continue
		}
		for _, msg := range res.Messages {
			msgType, _ := msg["type"].(string)
			if msgType == "error" {
				// Skip drivers that don't support groups_request.
				continue
			}
			if msgType == "group" {
				g := groupInfo{
					Arch:      res.Driver.Arch,
					SourceDir: msgStr(msg, "source_dir"),
					JID:       msgStr(msg, "jid"),
					Name:      msgStr(msg, "name"),
					Folder:    msgStr(msg, "folder"),
					Trigger:   msgStr(msg, "trigger"),
				}
				if v, ok := msg["is_main"].(bool); ok {
					g.IsMain = v
				}
				if v, ok := msg["requires_trigger"].(bool); ok {
					g.RequiresTrigger = v
				}
				groups = append(groups, g)
			}
		}
	}

	if groups == nil {
		groups = []groupInfo{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"groups": groups,
	})
}
