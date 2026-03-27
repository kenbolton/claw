// SPDX-License-Identifier: AGPL-3.0-or-later
package api

import (
	"net/http"
)

func (s *Server) handleArchs(w http.ResponseWriter, r *http.Request) {
	type driverInfo struct {
		Arch          string `json:"arch"`
		ArchVersion   string `json:"arch_version"`
		DriverVersion string `json:"driver_version"`
		DriverType    string `json:"driver_type"`
		Path          string `json:"path"`
	}

	drivers := make([]driverInfo, 0, len(s.Drivers))
	for _, d := range s.Drivers {
		drivers = append(drivers, driverInfo{
			Arch:          d.Arch,
			ArchVersion:   d.ArchVersion,
			DriverVersion: d.DriverVersion,
			DriverType:    d.DriverType,
			Path:          d.Path,
		})
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"drivers": drivers,
	})
}
