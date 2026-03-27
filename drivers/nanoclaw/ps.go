// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Apple Container timestamps are seconds since 2001-01-01 (Core Data epoch).
const appleEpochOffset = 978307200

func handlePs(sourceDir string) {
	if sourceDir == "" {
		sourceDir = findSourceDir()
	}

	runtime := detectRuntime()
	if runtime == "" {
		writeError("NO_RUNTIME", "neither 'container' nor 'docker' found")
		return
	}

	containers := fetchContainers(runtime)

	// Read groups from DB for joining
	groups, _ := readGroupRows(sourceDir)
	groupByFolder := map[string]*GroupRow{}
	for i := range groups {
		groupByFolder[groups[i].Folder] = &groups[i]
	}

	for _, c := range containers {
		// Filter out ephemeral agent containers (no nanoclaw- prefix = unnamed/UUID)
		if !strings.HasPrefix(c.id, "nanoclaw-") {
			continue
		}

		instance := map[string]interface{}{
			"type":  "instance",
			"id":    c.id,
			"arch":  arch,
			"state": c.state,
			"age":   c.age,
		}

		// Match container name to a group folder.
		// Container names are nanoclaw-<folder> or nanoclaw-<folder>-<suffix>
		// (e.g. nanoclaw-main-signal-1774566548315 → folder "main-signal").
		// Try longest-prefix match against known group folders.
		remainder := strings.TrimPrefix(c.id, "nanoclaw-")
		if g := matchGroupByPrefix(remainder, groupByFolder); g != nil {
			instance["group"] = g.Name
			instance["folder"] = g.Folder
			instance["jid"] = g.JID
			instance["is_main"] = g.IsMain
		} else {
			instance["group"] = remainder
			instance["folder"] = remainder
		}

		write(instance)
	}

	write(map[string]interface{}{
		"type":     "ps_complete",
		"warnings": []string{},
	})
}

type containerInfo struct {
	id    string
	state string
	age   string
}

func detectRuntime() string {
	for _, rt := range []string{"container", "docker"} {
		if _, err := exec.LookPath(rt); err == nil {
			return rt
		}
	}
	return ""
}

func fetchContainers(runtime string) []containerInfo {
	if runtime == "container" {
		return fetchAppleContainers()
	}
	return fetchDockerContainers()
}

func fetchAppleContainers() []containerInfo {
	out, err := exec.Command("container", "ls", "--format", "json").Output()
	if err != nil {
		return nil
	}

	var data []map[string]interface{}
	if err := json.Unmarshal(out, &data); err != nil {
		return nil
	}

	now := time.Now().Unix()
	var containers []containerInfo
	for _, c := range data {
		config, _ := c["configuration"].(map[string]interface{})
		cid, _ := config["id"].(string)
		imageMap, _ := config["image"].(map[string]interface{})
		image, _ := imageMap["reference"].(string)
		state, _ := c["status"].(string)

		if !strings.Contains(image, "nanoclaw-agent") && !strings.HasPrefix(cid, "nanoclaw-") {
			continue
		}

		age := "?"
		if sd, ok := c["startedDate"].(float64); ok {
			age = humanAge(now - (int64(sd) + appleEpochOffset))
		}

		containers = append(containers, containerInfo{id: cid, state: state, age: age})
	}
	return containers
}

func fetchDockerContainers() []containerInfo {
	out, err := exec.Command("docker", "ps", "--filter", "name=nanoclaw-", "--format", "json").Output()
	if err != nil {
		return nil
	}

	var containers []containerInfo
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var c map[string]string
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			continue
		}
		cid := c["Names"]
		if cid == "" {
			cid = c["ID"]
		}
		image := c["Image"]
		s := strings.ToLower(c["State"])
		if s == "" {
			s = strings.ToLower(c["Status"])
		}
		state := s
		if strings.Contains(s, "running") {
			state = "running"
		}
		age := strings.TrimSuffix(c["RunningFor"], " ago")

		if !strings.Contains(image, "nanoclaw-agent") && !strings.HasPrefix(cid, "nanoclaw-") {
			continue
		}
		containers = append(containers, containerInfo{id: cid, state: state, age: age})
	}
	return containers
}

// matchGroupByPrefix finds the group whose folder is the longest prefix of remainder.
// Handles nanoclaw-<folder>-<timestamp> style container names.
func matchGroupByPrefix(remainder string, groupByFolder map[string]*GroupRow) *GroupRow {
	// Exact match first
	if g, ok := groupByFolder[remainder]; ok {
		return g
	}
	// Longest prefix match: folder must be followed by '-' or end of string
	var best *GroupRow
	bestLen := 0
	for folder, g := range groupByFolder {
		if len(folder) <= bestLen {
			continue
		}
		if strings.HasPrefix(remainder, folder) && len(remainder) > len(folder) && remainder[len(folder)] == '-' {
			best = g
			bestLen = len(folder)
		}
	}
	return best
}

func humanAge(secs int64) string {
	if secs < 0 {
		secs = 0
	}
	if secs < 60 {
		return fmt.Sprintf("%ds", secs)
	}
	if secs < 3600 {
		m, s := secs/60, secs%60
		return fmt.Sprintf("%dm %02ds", m, s)
	}
	if secs < 86400 {
		h, m := secs/3600, (secs%3600)/60
		return fmt.Sprintf("%dh %02dm", h, m)
	}
	d, h := secs/86400, (secs%86400)/3600
	return fmt.Sprintf("%dd %02dh", d, h)
}
