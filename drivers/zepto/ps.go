// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

func handlePs(sourceDir string) {
	// sourceDir is not used: zepto running instances are discovered from
	// processes and containers, not from the data directory.
	_ = sourceDir
	instances := gatherInstances()

	for _, inst := range instances {
		write(inst)
	}

	write(map[string]interface{}{
		"type":     "ps_complete",
		"warnings": []string{},
	})
}

// gatherInstances returns all running ZeptoClaw instances, checking both
// native processes and container runtimes.
func gatherInstances() []map[string]interface{} {
	var instances []map[string]interface{}

	// 1. Native processes
	instances = append(instances, nativeInstances()...)

	// 2. Containerized (if a runtime is available)
	if rt := detectRuntime(); rt != "" {
		instances = append(instances, containerInstances(rt)...)
	}

	return instances
}

// nativeInstances finds running zeptoclaw gateway/daemon processes.
func nativeInstances() []map[string]interface{} {
	var procs []processInfo
	switch runtime.GOOS {
	case "darwin", "linux":
		procs = psGrep("zeptoclaw")
	}

	var instances []map[string]interface{}
	for _, p := range procs {
		// Only report gateway and daemon subcommands as "running instances"
		if !strings.Contains(p.cmd, "gateway") && !strings.Contains(p.cmd, "daemon") {
			continue
		}
		mode := "gateway"
		if strings.Contains(p.cmd, "daemon") {
			mode = "daemon"
		}
		instances = append(instances, map[string]interface{}{
			"type":  "instance",
			"id":    fmt.Sprintf("zepto-%s-%s", mode, p.pid),
			"arch":  arch,
			"state": "running",
			"age":   p.elapsed,
			"mode":  mode,
		})
	}
	return instances
}

type processInfo struct {
	pid     string
	cmd     string
	elapsed string
}

func psGrep(name string) []processInfo {
	var out []byte
	var err error

	if runtime.GOOS == "darwin" {
		out, err = exec.Command("ps", "-eo", "pid,etime,command").Output()
	} else {
		out, err = exec.Command("ps", "-eo", "pid,etimes,command", "--no-headers").Output()
	}
	if err != nil {
		return nil
	}

	var procs []processInfo
	lines := strings.Split(string(out), "\n")
	for _, line := range lines[1:] { // skip header on macOS
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		fullCmd := strings.Join(fields[2:], " ")
		if !strings.Contains(fullCmd, name) || strings.Contains(fullCmd, "grep") {
			continue
		}
		procs = append(procs, processInfo{
			pid:     fields[0],
			elapsed: fields[1],
			cmd:     fullCmd,
		})
	}
	return procs
}

// containerInstances finds zeptoclaw containers via docker or Apple Containers.
func containerInstances(rt string) []map[string]interface{} {
	if rt == "container" {
		return appleZeptoContainers()
	}
	return dockerZeptoContainers()
}

func detectRuntime() string {
	for _, rt := range []string{"container", "docker"} {
		if _, err := exec.LookPath(rt); err == nil {
			return rt
		}
	}
	return ""
}

const appleEpochOffset = 978307200

func appleZeptoContainers() []map[string]interface{} {
	out, err := exec.Command("container", "ls", "--format", "json").Output()
	if err != nil {
		return nil
	}
	var data []map[string]interface{}
	if err := json.Unmarshal(out, &data); err != nil {
		return nil
	}

	now := time.Now().Unix()
	var instances []map[string]interface{}
	for _, c := range data {
		config, _ := c["configuration"].(map[string]interface{})
		cid, _ := config["id"].(string)
		imageMap, _ := config["image"].(map[string]interface{})
		image, _ := imageMap["reference"].(string)
		state, _ := c["status"].(string)

		if !strings.Contains(image, "zeptoclaw") && !strings.HasPrefix(cid, "zeptoclaw-") {
			continue
		}

		age := "?"
		if sd, ok := c["startedDate"].(float64); ok {
			age = humanAge(now - (int64(sd) + appleEpochOffset))
		}

		instances = append(instances, map[string]interface{}{
			"type":          "instance",
			"id":            cid,
			"arch":          arch,
			"state":         state,
			"age":           age,
			"containerized": true,
		})
	}
	return instances
}

func dockerZeptoContainers() []map[string]interface{} {
	out, err := exec.Command("docker", "ps", "--filter", "name=zeptoclaw-", "--format", "json").Output()
	if err != nil {
		return nil
	}

	var instances []map[string]interface{}
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
		s := strings.ToLower(c["State"])
		if s == "" {
			s = strings.ToLower(c["Status"])
		}
		state := s
		if strings.Contains(s, "running") {
			state = "running"
		}
		age := c["RunningFor"]
		if strings.HasSuffix(age, " ago") {
			age = age[:len(age)-4]
		}

		if !strings.HasPrefix(cid, "zeptoclaw-") {
			continue
		}
		instances = append(instances, map[string]interface{}{
			"type":          "instance",
			"id":            cid,
			"arch":          arch,
			"state":         state,
			"age":           age,
			"containerized": true,
		})
	}
	return instances
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
