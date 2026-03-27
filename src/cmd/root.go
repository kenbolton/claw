// SPDX-License-Identifier: AGPL-3.0-or-later
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "claw",
	Short: "Universal CLI for claw agent architectures",
	Long: `
   _       /|
  | \_____/ |   claw
  |  _____> █   Universal CLI orchestrator for claw agent architectures
  |_/     \ |
           \|

Manage agents, watch conversations, and inspect running instances
across NanoClaw, OpenClaw, ZeptoClaw, PicoClaw, and others.

Examples:
  claw agent "What is 2+2?"
  claw agent -g dev "Review this code"
  claw ps
  claw watch -g main
  claw health
  claw archs
  claw api serve`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

var flagArch string

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&flagArch, "arch", "", "Target architecture (e.g. nanoclaw, zepto, openclaw, pico)")

	_ = rootCmd.RegisterFlagCompletionFunc("arch", completeArchs)
}
