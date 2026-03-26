// SPDX-License-Identifier: AGPL-3.0-or-later
package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kenbolton/claw/driver"
	"github.com/spf13/cobra"
)

var (
	flagPsJSON bool
)

var psCmd = &cobra.Command{
	Use:   "ps",
	Short: "List running claw agent instances",
	Long: `List running agent instances across all installed architectures.

Without --arch, queries all installed drivers and merges results.

Examples:
  claw ps
  claw ps --arch nanoclaw
  claw ps --json`,
	RunE: runPs,
}

func init() {
	psCmd.Flags().BoolVar(&flagPsJSON, "json", false, "Output raw JSON")
	rootCmd.AddCommand(psCmd)
}

func runPs(cmd *cobra.Command, args []string) error {
	var drivers []*driver.Driver

	if flagArch != "" {
		d, err := locateDriver(flagArch)
		if err != nil {
			return err
		}
		drivers = []*driver.Driver{d}
	} else {
		var err error
		drivers, err = driver.FindAll()
		if err != nil {
			return err
		}
		if len(drivers) == 0 {
			fmt.Println("No drivers installed. Run 'claw archs' for info.")
			return nil
		}
	}

	type instance struct {
		Arch   string
		ID     string
		Group  string
		Folder string
		JID    string
		State  string
		Age    string
		IsMain bool
	}

	var instances []instance
	var warnings []string

	for _, d := range drivers {
		req := map[string]interface{}{
			"type":       "ps_request",
			"source_dir": "",
		}

		scanner, wait, err := d.SendRequestAndClose(req)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", d.Arch, err))
			continue
		}

		for scanner.Scan() {
			var msg map[string]interface{}
			if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
				continue
			}
			msgType, _ := msg["type"].(string)
			switch msgType {
			case "instance":
				inst := instance{
					Arch:   str(msg, "arch"),
					ID:     str(msg, "id"),
					Group:  str(msg, "group"),
					Folder: str(msg, "folder"),
					JID:    str(msg, "jid"),
					State:  str(msg, "state"),
					Age:    str(msg, "age"),
				}
				if v, ok := msg["is_main"].(bool); ok {
					inst.IsMain = v
				}
				instances = append(instances, inst)
			case "ps_complete":
				if rawWarnings, ok := msg["warnings"].([]interface{}); ok {
					for _, w := range rawWarnings {
						if s, ok := w.(string); ok {
							warnings = append(warnings, s)
						}
					}
				}
			case "error":
				code, _ := msg["code"].(string)
				message, _ := msg["message"].(string)
				warnings = append(warnings, fmt.Sprintf("[%s] %s", code, message))
			}
		}
		_ = wait()
	}

	if flagPsJSON {
		data, _ := json.MarshalIndent(instances, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	if len(instances) == 0 {
		fmt.Println("No running instances.")
	} else {
		multiArch := len(drivers) > 1
		if multiArch {
			fmt.Printf("%-14s %-25s %-20s %-10s %s\n", "ARCH", "ID", "GROUP", "STATE", "AGE")
			fmt.Println(strings.Repeat("-", 80))
			for _, inst := range instances {
				main := ""
				if inst.IsMain {
					main = " [main]"
				}
				fmt.Printf("%-14s %-25s %-20s %-10s %s\n",
					inst.Arch, inst.ID, inst.Group+main, inst.State, inst.Age)
			}
		} else {
			fmt.Printf("%-25s %-20s %-10s %s\n", "ID", "GROUP", "STATE", "AGE")
			fmt.Println(strings.Repeat("-", 65))
			for _, inst := range instances {
				main := ""
				if inst.IsMain {
					main = " [main]"
				}
				fmt.Printf("%-25s %-20s %-10s %s\n",
					inst.ID, inst.Group+main, inst.State, inst.Age)
			}
		}
	}

	for _, w := range warnings {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", w)
	}
	return nil
}

func str(m map[string]interface{}, key string) string {
	v, _ := m[key].(string)
	return v
}
