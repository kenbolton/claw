// SPDX-License-Identifier: AGPL-3.0-or-later
package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/spf13/cobra"
)

var moltCmd = &cobra.Command{
	Use:                "molt [args...]",
	Short:              "Run molt (migration tool) if installed",
	Long:               `Passthrough to the molt binary for agent migration between architectures.`,
	DisableFlagParsing: true,
	SilenceUsage:       true,
	RunE:               runMolt,
}

func init() {
	rootCmd.AddCommand(moltCmd)
}

func runMolt(_ *cobra.Command, args []string) error {
	bin, err := exec.LookPath("molt")
	if err != nil {
		return fmt.Errorf("molt not found in PATH\n\nInstall: https://github.com/kenbolton/molt/releases")
	}

	// exec replaces the current process — no child process overhead
	return syscall.Exec(bin, append([]string{"molt"}, args...), os.Environ())
}
