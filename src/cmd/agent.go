// SPDX-License-Identifier: AGPL-3.0-or-later
package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

var (
	flagGroup   string
	flagJID     string
	flagSession string
	flagFile    string
	flagPipe    bool
	flagNative  bool
	flagVerbose bool
)

var agentCmd = &cobra.Command{
	Use:   "agent [prompt]",
	Short: "Send a prompt to a claw agent",
	Long: `Send a prompt to a claw agent container.

The prompt can be provided as a positional argument, read from a file
with -f, or piped via stdin with --pipe.

Examples:
  claw agent "What is 2+2?"
  claw agent -g dev "Review this code"
  claw agent -s <session-id> "Continue"
  echo "prompt" | claw agent --pipe -g main
  claw agent -f tasks.txt`,
	Args: cobra.MaximumNArgs(1),
	RunE: runAgent,
}

func init() {
	agentCmd.Flags().StringVarP(&flagGroup, "group", "g", "", "Target group by name or folder (fuzzy match)")
	agentCmd.Flags().StringVarP(&flagJID, "jid", "j", "", "Target group by exact JID")
	agentCmd.Flags().StringVarP(&flagSession, "session", "s", "", "Session ID to resume")
	agentCmd.Flags().StringVarP(&flagFile, "file", "f", "", "Read prompt from a file")
	agentCmd.Flags().BoolVarP(&flagPipe, "pipe", "p", false, "Read prompt from stdin")
	agentCmd.Flags().BoolVar(&flagNative, "native", false, "Run agent natively without a container (dev mode, no sandbox)")
	agentCmd.Flags().BoolVar(&flagVerbose, "verbose", false, "Show agent-runner diagnostic output")

	_ = agentCmd.RegisterFlagCompletionFunc("group", func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return nil, cobra.ShellCompDirectiveNoFileComp
	})

	rootCmd.AddCommand(agentCmd)
}

func runAgent(cmd *cobra.Command, args []string) error {
	// Resolve architecture
	archName := flagArch
	if archName == "" {
		archName = "nanoclaw" // default for now
	}

	d, err := locateDriver(archName)
	if err != nil {
		return err
	}

	// Build prompt
	prompt, err := resolvePrompt(args)
	if err != nil {
		return err
	}

	req := map[string]interface{}{
		"type":       "agent_request",
		"source_dir": "",
		"group":      flagGroup,
		"jid":        flagJID,
		"prompt":     prompt,
		"session_id": flagSession,
		"resume_at":  "",
		"native":     flagNative,
		"verbose":    flagVerbose,
	}

	scanner, wait, err := d.SendRequestAndClose(req)
	if err != nil {
		return err
	}

	for scanner.Scan() {
		var msg map[string]interface{}
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}
		msgType, _ := msg["type"].(string)
		switch msgType {
		case "agent_output":
			text, _ := msg["text"].(string)
			chunk, _ := msg["chunk"].(bool)
			if chunk {
				// Streaming chunk — print without extra newline
				fmt.Print(text)
			} else {
				fmt.Println(text)
			}
		case "agent_complete":
			status, _ := msg["status"].(string)
			sessionID, _ := msg["session_id"].(string)
			if sessionID != "" {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "\n[session: %s]\n", sessionID)
			}
			if status != "success" && status != "" {
				message, _ := msg["message"].(string)
				if message != "" {
					return fmt.Errorf("agent %s: %s", status, message)
				}
				return fmt.Errorf("agent status: %s", status)
			}
		case "error":
			code, _ := msg["code"].(string)
			message, _ := msg["message"].(string)
			return fmt.Errorf("[%s] %s", code, message)
		}
	}

	return wait()
}

func resolvePrompt(args []string) (string, error) {
	var parts []string

	if len(args) > 0 {
		parts = append(parts, args[0])
	}

	if flagFile != "" {
		data, err := os.ReadFile(flagFile)
		if err != nil {
			return "", fmt.Errorf("failed to read file %s: %w", flagFile, err)
		}
		parts = append(parts, strings.TrimSpace(string(data)))
	}

	if flagPipe {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("failed to read stdin: %w", err)
		}
		parts = append(parts, strings.TrimSpace(string(data)))
	}

	prompt := strings.Join(parts, "\n\n")
	if prompt == "" {
		return "", fmt.Errorf("no prompt provided — pass as argument, use -f, or --pipe")
	}
	return prompt, nil
}
