// SPDX-License-Identifier: AGPL-3.0-or-later
package main

// handleGroups returns UNSUPPORTED — ZeptoClaw does not have a groups database.
func handleGroups(_ map[string]interface{}) {
	writeError("UNSUPPORTED", "zepto driver does not support groups_request")
}
