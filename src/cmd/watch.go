// SPDX-License-Identifier: AGPL-3.0-or-later
package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"github.com/spf13/cobra"
)

var (
	flagWatchGroup string
	flagWatchJID   string
	flagWatchLines int
)

var watchCmd = &cobra.Command{
	Use:   "watch",
	Short: "Watch agent messages in real time",
	Long: `Poll the database for new messages and print them as they arrive.

Shows the last N messages as history, then streams new messages
until interrupted with Ctrl-C.

Examples:
  claw watch -g main
  claw watch -g main -n 5
  claw watch -j <jid>`,
	RunE: runWatch,
}

func init() {
	watchCmd.Flags().StringVarP(&flagWatchGroup, "group", "g", "", "Group name or folder (fuzzy match)")
	watchCmd.Flags().StringVarP(&flagWatchJID, "jid", "j", "", "Chat JID (exact)")
	watchCmd.Flags().IntVarP(&flagWatchLines, "lines", "n", 20, "Number of history lines to show")

	rootCmd.AddCommand(watchCmd)
}

func runWatch(cmd *cobra.Command, args []string) error {
	archName := flagArch
	if archName == "" {
		archName = "nanoclaw"
	}

	d, err := locateDriver(archName)
	if err != nil {
		return err
	}

	req := map[string]interface{}{
		"type":       "watch_request",
		"source_dir": "",
		"group":      flagWatchGroup,
		"jid":        flagWatchJID,
		"lines":      flagWatchLines,
	}

	scanner, stdin, wait, err := d.StreamRequest(req)
	if err != nil {
		return err
	}

	// Handle Ctrl-C: close stdin to signal driver to exit
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		<-sigCh
		_ = stdin.Close()
	}()

	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Watching (Ctrl-C to stop)...\n")

	for scanner.Scan() {
		var msg map[string]interface{}
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}
		msgType, _ := msg["type"].(string)
		switch msgType {
		case "message":
			ts, _ := msg["timestamp"].(string)
			sender, _ := msg["sender"].(string)
			content, _ := msg["content"].(string)

			tsDisplay := ts
			if len(ts) >= 19 {
				// Extract HH:MM:SS from ISO timestamp
				tsDisplay = ts[11:19]
			}

			display := strings.ReplaceAll(content, "\n", " ")
			if len(display) > 120 {
				display = display[:117] + "..."
			}
			fmt.Printf("[%s] %s: %s\n", tsDisplay, sender, display)
		case "error":
			code, _ := msg["code"].(string)
			message, _ := msg["message"].(string)
			return fmt.Errorf("[%s] %s", code, message)
		}
	}

	_ = stdin.Close()
	_ = wait()
	return nil
}
