// SPDX-License-Identifier: AGPL-3.0-or-later
package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"time"

	"github.com/kenbolton/claw/driver"
	"github.com/spf13/cobra"
)

var (
	flagHealthJSON     bool
	flagHealthGroup    string
	flagHealthWatch    bool
	flagHealthInterval int
	flagHealthFailFast bool
)

var healthCmd = &cobra.Command{
	Use:   "health",
	Short: "Run health diagnostics on claw installations",
	Long: `Run health checks against one or more claw agent installations.

Checks runtime, credentials, database, disk, sessions, groups, skills, and
image status. Reports pass/warn/fail per check with an overall summary.

Examples:
  claw health
  claw health --arch nanoclaw
  claw health -g main
  claw health --watch
  claw health --watch --interval 60
  claw health --json
  claw health --fail-fast`,
	RunE: runHealth,
}

func init() {
	healthCmd.Flags().BoolVar(&flagHealthJSON, "json", false, "Emit NDJSON instead of formatted output")
	healthCmd.Flags().StringVarP(&flagHealthGroup, "group", "g", "", "Check a specific group within an installation")
	healthCmd.Flags().BoolVar(&flagHealthWatch, "watch", false, "Re-run every --interval seconds")
	healthCmd.Flags().IntVar(&flagHealthInterval, "interval", 30, "Polling interval in seconds (used with --watch)")
	healthCmd.Flags().BoolVar(&flagHealthFailFast, "fail-fast", false, "Exit 1 on first failed check")

	rootCmd.AddCommand(healthCmd)
}

// checkResult holds the result of a single health check.
type checkResult struct {
	Arch        string `json:"arch"`
	SourceDir   string `json:"source_dir,omitempty"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	Detail      string `json:"detail"`
	Remediation string `json:"remediation,omitempty"`
}

func runHealth(cmd *cobra.Command, args []string) error {
	if flagHealthWatch {
		return runHealthWatch(cmd)
	}
	code := runHealthOnce(cmd)
	if code != 0 {
		os.Exit(code)
	}
	return nil
}

func runHealthWatch(cmd *cobra.Command) error {
	interval := time.Duration(flagHealthInterval) * time.Second

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)

	for {
		if !flagHealthJSON {
			// ANSI clear screen.
			fmt.Print("\033[2J\033[H")
		}

		code := runHealthOnce(cmd)

		select {
		case <-sigCh:
			os.Exit(code)
		case <-time.After(interval):
		}
	}
}

// runHealthOnce executes one round of health checks and returns an exit code.
func runHealthOnce(cmd *cobra.Command) int {
	drivers, err := resolveHealthDrivers()
	if err != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "error: %v\n", err)
		return 3
	}
	if len(drivers) == 0 {
		if !flagHealthJSON {
			fmt.Println("No drivers installed. Run 'claw archs' for info.")
		}
		return 3
	}

	var allResults []checkResult
	driverError := false

	for _, d := range drivers {
		results, unsupported := queryDriverHealth(d)
		if unsupported {
			driverError = true
			// Fallback: run partial checks directly.
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
				"warning: %s driver does not support health checks; showing partial results\n", d.Arch)
			results = fallbackChecks(d)
		}
		allResults = append(allResults, results...)

		// Output results for this driver.
		if flagHealthJSON {
			for _, r := range results {
				data, _ := json.Marshal(map[string]interface{}{
					"type":       "check",
					"arch":       r.Arch,
					"source_dir": r.SourceDir,
					"name":       r.Name,
					"status":     r.Status,
					"detail":     r.Detail,
				})
				fmt.Println(string(data))
			}
		}

		if flagHealthFailFast {
			for i, r := range results {
				if r.Status == "fail" {
					if !flagHealthJSON {
						// Show results up to and including the first failure.
						printHumanResults(d, results[:i+1])
					}
					return 1
				}
			}
		}

		if !flagHealthJSON {
			printHumanResults(d, results)
		}
	}

	// Summary.
	pass, warn, fail := countStatuses(allResults)
	if flagHealthJSON {
		for _, d := range drivers {
			data, _ := json.Marshal(map[string]interface{}{
				"type":       "health_complete",
				"arch":       d.Arch,
				"source_dir": "",
				"pass":       pass,
				"warn":       warn,
				"fail":       fail,
			})
			fmt.Println(string(data))
			break // one summary line for all
		}
	} else {
		fmt.Println()
		overall := "PASS"
		if fail > 0 {
			overall = "FAIL"
		} else if warn > 0 {
			overall = "WARN"
		}
		var parts []string
		if fail > 0 {
			parts = append(parts, fmt.Sprintf("%d error", fail))
			if fail > 1 {
				parts[len(parts)-1] += "s"
			}
		}
		if warn > 0 {
			parts = append(parts, fmt.Sprintf("%d warning", warn))
			if warn > 1 {
				parts[len(parts)-1] += "s"
			}
		}
		summary := ""
		if len(parts) > 0 {
			summary = fmt.Sprintf("  (%s)", strings.Join(parts, ", "))
		}
		fmt.Printf("Overall: %s%s\n", overall, summary)
	}

	return computeExitCode(allResults, driverError)
}

func resolveHealthDrivers() ([]*driver.Driver, error) {
	if flagArch != "" {
		d, err := locateDriver(flagArch)
		if err != nil {
			return nil, err
		}
		return []*driver.Driver{d}, nil
	}
	// Default: check all installed drivers (matches claw ps behavior).
	return driver.FindAll()
}

// queryDriverHealth sends a health_request to the driver and collects results.
// Returns the results and whether the driver returned UNSUPPORTED.
func queryDriverHealth(d *driver.Driver) ([]checkResult, bool) {
	req := map[string]interface{}{
		"type":       "health_request",
		"source_dir": "",
		"group":      flagHealthGroup,
		"checks":     []string{},
	}

	scanner, wait, err := d.SendRequestAndClose(req)
	if err != nil {
		return []checkResult{{
			Arch:   d.Arch,
			Name:   "driver",
			Status: "fail",
			Detail: fmt.Sprintf("cannot communicate with driver: %v", err),
		}}, false
	}

	var results []checkResult
	for scanner.Scan() {
		var msg map[string]interface{}
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}
		msgType, _ := msg["type"].(string)
		switch msgType {
		case "check_result":
			r := checkResult{
				Arch:        d.Arch,
				Name:        str(msg, "name"),
				Status:      str(msg, "status"),
				Detail:      str(msg, "detail"),
				Remediation: str(msg, "remediation"),
			}
			results = append(results, r)
		case "health_complete":
			// Driver is done.
		case "error":
			code := str(msg, "code")
			if code == "UNSUPPORTED" {
				_ = wait()
				return nil, true
			}
			message := str(msg, "message")
			results = append(results, checkResult{
				Arch:   d.Arch,
				Name:   "driver",
				Status: "fail",
				Detail: fmt.Sprintf("[%s] %s", code, message),
			})
		}
	}
	_ = wait()
	return results, false
}

// fallbackChecks runs basic checks without driver support.
func fallbackChecks(d *driver.Driver) []checkResult {
	var results []checkResult

	// Runtime check: can we find docker or container CLI?
	rtStatus, rtDetail := "fail", "no container runtime found"
	for _, rt := range []string{"container", "docker"} {
		if _, err := lookPath(rt); err == nil {
			rtStatus = "pass"
			rtDetail = fmt.Sprintf("%s available", rt)
			break
		}
	}
	results = append(results, checkResult{
		Arch:   d.Arch,
		Name:   "runtime",
		Status: rtStatus,
		Detail: rtDetail,
	})

	// Credentials check: environment variables.
	if token := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"); token != "" {
		results = append(results, checkResult{
			Arch:   d.Arch,
			Name:   "credentials",
			Status: "pass",
			Detail: "CLAUDE_CODE_OAUTH_TOKEN present (env)",
		})
	} else if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		results = append(results, checkResult{
			Arch:   d.Arch,
			Name:   "credentials",
			Status: "pass",
			Detail: "ANTHROPIC_API_KEY present (env)",
		})
	} else {
		results = append(results, checkResult{
			Arch:        d.Arch,
			Name:        "credentials",
			Status:      "fail",
			Detail:      "no API credentials found in environment",
			Remediation: "Set CLAUDE_CODE_OAUTH_TOKEN or ANTHROPIC_API_KEY",
		})
	}

	return results
}

func printHumanResults(d *driver.Driver, results []checkResult) {
	fmt.Printf("%s  %s\n", d.Arch, d.Path)
	for _, r := range results {
		symbol := statusSymbol(r.Status)
		fmt.Printf("  %s  %-16s %s\n", symbol, r.Name, r.Detail)
	}
}

func statusSymbol(status string) string {
	switch status {
	case "pass":
		return "\u2713" // ✓
	case "warn":
		return "\u26A0" // ⚠
	case "fail":
		return "\u2717" // ✗
	default:
		return "?"
	}
}

func countStatuses(results []checkResult) (pass, warn, fail int) {
	for _, r := range results {
		switch r.Status {
		case "pass":
			pass++
		case "warn":
			warn++
		case "fail":
			fail++
		}
	}
	return
}

// computeExitCode returns the appropriate exit code based on check results.
func computeExitCode(results []checkResult, driverError bool) int {
	if driverError && len(results) == 0 {
		return 3
	}
	pass, warn, fail := countStatuses(results)
	_ = pass
	if fail > 0 {
		return 1
	}
	if warn > 0 {
		return 2
	}
	return 0
}

// lookPath wraps exec.LookPath for testability.
var lookPath = lookPathDefault

func lookPathDefault(name string) (string, error) {
	return exec.LookPath(name)
}
