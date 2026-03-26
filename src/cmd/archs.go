// SPDX-License-Identifier: AGPL-3.0-or-later
package cmd

import (
	"fmt"
	"strings"

	"github.com/kenbolton/claw/driver"
	"github.com/spf13/cobra"
)

var archsCmd = &cobra.Command{
	Use:   "archs",
	Short: "List installed claw architecture drivers",
	Long: `List claw drivers found in $PATH or ~/.claw/drivers/.

Each claw architecture ships its own claw-driver-<arch> binary.
Install them to enable commands for that architecture.

Examples:
  claw archs`,
	RunE: runArchs,
}

func init() {
	rootCmd.AddCommand(archsCmd)
}

func runArchs(cmd *cobra.Command, args []string) error {
	drivers, err := driver.FindAll()
	if err != nil {
		return err
	}

	if len(drivers) == 0 {
		fmt.Println("No drivers found.")
		fmt.Println()
		fmt.Println("Install a driver to your PATH or ~/.claw/drivers/:")
		fmt.Println("  claw-driver-nanoclaw   (ships with NanoClaw)")
		fmt.Println("  claw-driver-zepto      (ships with ZeptoClaw)")
		fmt.Println("  claw-driver-openclaw   (ships with OpenClaw)")
		fmt.Println("  claw-driver-pico       (ships with PicoClaw)")
		return nil
	}

	fmt.Printf("%-20s %-12s %-12s %s\n", "ARCH", "ARCH VER", "DRIVER VER", "PATH")
	fmt.Println(strings.Repeat("-", 70))
	for _, d := range drivers {
		fmt.Printf("%-20s %-12s %-12s %s\n", d.Arch, d.ArchVersion, d.DriverVersion, d.Path)
	}
	return nil
}
