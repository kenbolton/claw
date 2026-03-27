// SPDX-License-Identifier: AGPL-3.0-or-later
package api

import (
	"net/http"
	"strings"

	"github.com/kenbolton/claw/driver"
)

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	arch := r.URL.Query().Get("arch")
	group := r.URL.Query().Get("group")
	checksParam := r.URL.Query().Get("checks")

	drivers := s.filterDrivers(arch)
	if len(drivers) == 0 {
		writeError(w, http.StatusNotFound, "ARCH_NOT_FOUND", "no driver found for arch")
		return
	}

	var checks []string
	if checksParam != "" {
		checks = strings.Split(checksParam, ",")
	}

	results := fanOut(drivers, func(d *driver.Driver) map[string]interface{} {
		return map[string]interface{}{
			"type":       "health_request",
			"source_dir": s.SourceDir,
			"group":      group,
			"checks":     checks,
		}
	})

	type checkInfo struct {
		Name        string `json:"name"`
		Status      string `json:"status"`
		Detail      string `json:"detail"`
		Remediation string `json:"remediation,omitempty"`
	}

	type installation struct {
		Arch      string      `json:"arch"`
		SourceDir string      `json:"source_dir"`
		Checks    []checkInfo `json:"checks"`
		Summary   struct {
			Pass int `json:"pass"`
			Warn int `json:"warn"`
			Fail int `json:"fail"`
		} `json:"summary"`
		Overall string `json:"overall"`
	}

	var installations []installation
	allFailed := true

	for _, res := range results {
		if res.Err != nil {
			continue
		}
		allFailed = false

		inst := installation{
			Arch:      res.Driver.Arch,
			SourceDir: s.SourceDir,
		}

		for _, msg := range res.Messages {
			msgType, _ := msg["type"].(string)
			if msgType == "check_result" {
				c := checkInfo{
					Name:        msgStr(msg, "name"),
					Status:      msgStr(msg, "status"),
					Detail:      msgStr(msg, "detail"),
					Remediation: msgStr(msg, "remediation"),
				}
				inst.Checks = append(inst.Checks, c)
				switch c.Status {
				case "pass":
					inst.Summary.Pass++
				case "warn":
					inst.Summary.Warn++
				case "fail":
					inst.Summary.Fail++
				}
			}
		}

		if inst.Checks == nil {
			inst.Checks = []checkInfo{}
		}

		inst.Overall = "pass"
		if inst.Summary.Fail > 0 {
			inst.Overall = "fail"
		} else if inst.Summary.Warn > 0 {
			inst.Overall = "warn"
		}

		installations = append(installations, inst)
	}

	if allFailed && len(results) > 0 {
		writeError(w, http.StatusBadGateway, "DRIVER_ERROR", "all drivers failed")
		return
	}

	if installations == nil {
		installations = []installation{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"installations": installations,
	})
}
