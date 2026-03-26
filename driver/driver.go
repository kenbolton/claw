// SPDX-License-Identifier: AGPL-3.0-or-later
// Package driver implements the claw driver protocol.
// Drivers are standalone binaries (claw-driver-<arch>) that communicate
// via newline-delimited JSON on stdin/stdout.
package driver

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Driver is a located and version-verified arch driver.
type Driver struct {
	Arch           string
	ArchVersion    string
	DriverVersion  string
	DriverType     string // "local" or "remote"
	RequiresConfig []string
	Path           string
}

// FindAll locates all claw-driver-* binaries in ~/.claw/drivers/ and $PATH.
func FindAll() ([]*Driver, error) {
	var drivers []*Driver
	seen := map[string]bool{}

	for _, dir := range searchPaths() {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !strings.HasPrefix(e.Name(), "claw-driver-") {
				continue
			}
			arch := strings.TrimPrefix(e.Name(), "claw-driver-")
			fullPath := filepath.Join(dir, e.Name())
			if seen[arch] {
				continue
			}
			seen[arch] = true
			d, err := probeDriver(arch, fullPath)
			if err != nil {
				continue
			}
			drivers = append(drivers, d)
		}
	}
	return drivers, nil
}

// Locate finds the driver for a specific arch.
// Pass sourceDir to allow the driver to detect the installed arch version.
func Locate(arch string, sourceDir ...string) (*Driver, error) {
	name := "claw-driver-" + arch
	src := ""
	if len(sourceDir) > 0 {
		src = sourceDir[0]
	}

	// Check ~/.claw/drivers/ and $PATH
	for _, dir := range searchPaths() {
		fullPath := filepath.Join(dir, name)
		if _, err := os.Stat(fullPath); err == nil {
			return probeDriver(arch, fullPath, src)
		}
	}
	// Also try exec.LookPath
	if path, err := exec.LookPath(name); err == nil {
		return probeDriver(arch, path, src)
	}

	return nil, fmt.Errorf(
		"driver not found for arch %q\n\nInstall claw-driver-%s to $PATH or ~/.claw/drivers/\n\nInstalled drivers:\n  claw archs",
		arch, arch,
	)
}

// DetectArch probes all installed local drivers and returns the best match.
func DetectArch(sourceDir string) (string, error) {
	drivers, err := FindAll()
	if err != nil {
		return "", err
	}

	type candidate struct {
		arch       string
		confidence float64
	}
	var best candidate

	for _, d := range drivers {
		if d.DriverType == "remote" {
			continue
		}
		c, err := probeConfidence(d, sourceDir)
		if err != nil {
			continue
		}
		if c > best.confidence {
			best = candidate{arch: d.Arch, confidence: c}
		}
	}

	if best.confidence == 0 {
		return "", fmt.Errorf(
			"could not detect arch for %q\n\nUse --arch to specify: --arch <nanoclaw|zepto|openclaw|pico>",
			sourceDir,
		)
	}
	return best.arch, nil
}

// SendRequest sends an NDJSON request to the driver and returns a scanner for
// reading response lines. The caller must call wait() when done to clean up.
func (d *Driver) SendRequest(req map[string]interface{}) (*bufio.Scanner, func() error, error) {
	reqJSON, _ := json.Marshal(req)

	cmd := exec.Command(d.Path)
	cmd.Stderr = os.Stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("failed to start driver: %w", err)
	}

	// Write request and leave stdin open (driver may read stdin close as signal)
	_, _ = stdin.Write(reqJSON)
	_, _ = stdin.Write([]byte("\n"))

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1024*1024), 200*1024*1024)

	wait := func() error {
		_ = stdin.Close()
		return cmd.Wait()
	}

	return scanner, wait, nil
}

// SendRequestAndClose sends an NDJSON request, closes stdin, and returns a
// scanner for reading response lines.
func (d *Driver) SendRequestAndClose(req map[string]interface{}) (*bufio.Scanner, func() error, error) {
	reqJSON, _ := json.Marshal(req)

	cmd := exec.Command(d.Path)
	cmd.Stderr = os.Stderr
	cmd.Stdin = strings.NewReader(string(reqJSON) + "\n")

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("failed to start driver: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1024*1024), 200*1024*1024)

	return scanner, cmd.Wait, nil
}

// StreamRequest sends an NDJSON request and keeps stdin open, returning the
// stdin writer so the caller can close it to signal the driver to exit.
func (d *Driver) StreamRequest(req map[string]interface{}) (*bufio.Scanner, io.WriteCloser, func() error, error) {
	reqJSON, _ := json.Marshal(req)

	cmd := exec.Command(d.Path)
	cmd.Stderr = os.Stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to get stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to get stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, nil, nil, fmt.Errorf("failed to start driver: %w", err)
	}

	_, _ = stdin.Write(reqJSON)
	_, _ = stdin.Write([]byte("\n"))

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1024*1024), 200*1024*1024)

	return scanner, stdin, cmd.Wait, nil
}

// probeDriver calls the driver's version endpoint.
func probeDriver(arch, path string, sourceDir ...string) (*Driver, error) {
	req := map[string]interface{}{"type": "version_request"}
	if len(sourceDir) > 0 && sourceDir[0] != "" {
		req["source_dir"] = sourceDir[0]
	}
	reqJSON, _ := json.Marshal(req)
	cmd := exec.Command(path)
	cmd.Stdin = strings.NewReader(string(reqJSON) + "\n")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("driver %s version check failed: %w", path, err)
	}

	var resp struct {
		Type           string   `json:"type"`
		Arch           string   `json:"arch"`
		ArchVersion    string   `json:"arch_version"`
		DriverVersion  string   `json:"driver_version"`
		DriverType     string   `json:"driver_type"`
		RequiresConfig []string `json:"requires_config"`
	}
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		if err := json.Unmarshal(scanner.Bytes(), &resp); err == nil && resp.Type == "version_response" {
			break
		}
	}
	if resp.Arch == "" {
		return nil, fmt.Errorf("driver %s returned no version_response", path)
	}
	if resp.DriverType == "" {
		resp.DriverType = "local"
	}
	return &Driver{
		Arch:           resp.Arch,
		ArchVersion:    resp.ArchVersion,
		DriverVersion:  resp.DriverVersion,
		DriverType:     resp.DriverType,
		RequiresConfig: resp.RequiresConfig,
		Path:           path,
	}, nil
}

// probeConfidence asks a driver how confident it is about a source directory.
func probeConfidence(d *Driver, sourceDir string) (float64, error) {
	req, _ := json.Marshal(map[string]string{
		"type":       "probe_request",
		"source_dir": sourceDir,
	})
	cmd := exec.Command(d.Path)
	cmd.Stdin = strings.NewReader(string(req) + "\n")
	out, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	var resp struct {
		Type       string  `json:"type"`
		Confidence float64 `json:"confidence"`
	}
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		if err := json.Unmarshal(scanner.Bytes(), &resp); err == nil && resp.Type == "probe_response" {
			return resp.Confidence, nil
		}
	}
	return 0, nil
}

func searchPaths() []string {
	var paths []string
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".claw", "drivers"))
	}
	paths = append(paths, filepath.SplitList(os.Getenv("PATH"))...)
	return paths
}
