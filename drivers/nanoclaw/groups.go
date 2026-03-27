// SPDX-License-Identifier: AGPL-3.0-or-later
package main

// handleGroups lists registered groups from the database.
func handleGroups(msg map[string]interface{}) {
	sourceDir, _ := msg["source_dir"].(string)
	if sourceDir == "" {
		sourceDir = findSourceDir()
	}

	groups, err := readGroupRows(sourceDir)
	if err != nil {
		writeError("DB_ERROR", err.Error())
		return
	}

	for _, g := range groups {
		trigger := g.TriggerPattern
		write(map[string]interface{}{
			"type":             "group",
			"source_dir":       sourceDir,
			"jid":              g.JID,
			"name":             g.Name,
			"folder":           g.Folder,
			"trigger":          trigger,
			"is_main":          g.IsMain,
			"requires_trigger": g.RequiresTrigger,
		})
	}

	write(map[string]interface{}{
		"type": "groups_complete",
	})
}
