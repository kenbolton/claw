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
	flagGroup     string
	flagJID       string
	flagSession   string
	flagFile      string
	flagPipe      bool
	flagNative    bool
	flagVerbose   bool
	flagTimeout   string
	flagTemplate  string
	flagEphemeral bool
)

var agentCmd = &cobra.Command{
	Use:   "agent [prompt]",
	Short: "Send a prompt to a claw agent",
	Long: `Send a prompt to a claw agent container.

The prompt can be provided as a positional argument, read from a file
with -f, or piped via stdin with --pipe.

Use --template to control where piped/file content appears in the prompt:
  echo "data" | claw agent --pipe --template "Here is the data:\n\n{input}\n\nSummarise."

Exit codes: 0=success, 1=error, 2=timeout.

Examples:
  claw agent "What is 2+2?"
  claw agent -g dev "Review this code"
  claw agent -s <session-id> "Continue"
  echo "prompt" | claw agent --pipe -g main
  claw agent -f tasks.txt
  git diff | claw agent --native --pipe -g dev "review this diff"
  echo "data" | claw agent --native --pipe --timeout 30s --template "Analyse: {input}"
  echo "one-off" | claw agent --native --pipe --ephemeral "process this"`,
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
	agentCmd.Flags().StringVar(&flagTimeout, "timeout", "5m", "Max duration for agent response (e.g. 30s, 5m)")
	agentCmd.Flags().StringVarP(&flagTemplate, "template", "t", "", "Prompt template with {input} placeholder for piped/file content")
	agentCmd.Flags().BoolVar(&flagEphemeral, "ephemeral", false, "Use a disposable workspace (no session persistence)")

	_ = agentCmd.RegisterFlagCompletionFunc("group", func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return nil, cobra.ShellCompDirectiveNoFileComp
	})

	rootCmd.AddCommand(agentCmd)
}

func runAgent(cmd *cobra.Command, args []string) error {
	// Validate flag combinations.
	if flagEphemeral && flagSession != "" {
		return fmt.Errorf("--ephemeral and --session are mutually exclusive")
	}

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
		"timeout":    flagTimeout,
		"ephemeral":  flagEphemeral,
	}

	scanner, wait, err := d.SendRequestAndClose(req)
	if err != nil {
		return err
	}

	var agentErr error
	gotComplete := false

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
				fmt.Print(text)
			} else {
				fmt.Println(text)
			}
		case "agent_complete":
			gotComplete = true
			status, _ := msg["status"].(string)
			sessionID, _ := msg["session_id"].(string)
			if sessionID != "" {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "\n[session: %s]\n", sessionID)
			}
			if status == "timeout" {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "agent timed out\n")
				_ = wait()
				os.Exit(2)
			}
			if status != "success" && status != "" {
				message, _ := msg["message"].(string)
				if message != "" {
					agentErr = fmt.Errorf("agent %s: %s", status, message)
				} else {
					agentErr = fmt.Errorf("agent status: %s", status)
				}
			}
		case "error":
			code, _ := msg["code"].(string)
			message, _ := msg["message"].(string)
			return fmt.Errorf("[%s] %s", code, message)
		}
	}

	_ = wait()

	if !gotComplete {
		return fmt.Errorf("driver exited without sending agent_complete")
	}
	return agentErr
}

func resolvePrompt(args []string) (string, error) {
	if flagTemplate != "" {
		// Template mode: collect input from file/stdin, substitute into template.
		var inputParts []string
		if flagFile != "" {
			data, err := os.ReadFile(flagFile)
			if err != nil {
				return "", fmt.Errorf("failed to read file %s: %w", flagFile, err)
			}
			inputParts = append(inputParts, strings.TrimSpace(string(data)))
		}
		if flagPipe {
			data, err := io.ReadAll(os.Stdin)
			if err != nil {
				return "", fmt.Errorf("failed to read stdin: %w", err)
			}
			inputParts = append(inputParts, strings.TrimSpace(string(data)))
		}
		if len(args) > 0 && len(inputParts) == 0 {
			// Use positional arg as input if no file/pipe content.
			inputParts = append(inputParts, args[0])
		}
		input := strings.Join(inputParts, "\n\n")
		if input == "" {
			return "", fmt.Errorf("--template requires input from --pipe, -f, or a positional argument")
		}
		return strings.ReplaceAll(flagTemplate, "{input}", input), nil
	}

	// Standard mode: join all parts with double newline.
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
