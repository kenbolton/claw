// SPDX-License-Identifier: AGPL-3.0-or-later
package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

var replCmd = &cobra.Command{
	Use:   "repl",
	Short: "Start an interactive REPL session with a claw agent",
	Long: `Start an interactive REPL session with a claw agent.

Maintains conversation context across prompts using a local history buffer,
so the agent remembers the conversation regardless of architecture.
Session IDs are also threaded through for architectures that support
native session persistence (e.g. gateway mode).

Slash commands: /new  /session  /history  /exit  /help

Examples:
  claw repl
  claw repl -g main
  claw repl -g dev --arch zepto
  claw repl -s <session-id>    # resume a gateway session`,
	Args: cobra.NoArgs,
	RunE: runRepl,
}

func init() {
	replCmd.Flags().StringVarP(&flagGroup, "group", "g", "", "Target group by name or folder (fuzzy match)")
	replCmd.Flags().StringVarP(&flagJID, "jid", "j", "", "Target group by exact JID")
	replCmd.Flags().StringVarP(&flagSession, "session", "s", "", "Session ID to resume (gateway mode)")
	replCmd.Flags().BoolVar(&flagNative, "native", false, "Run agent natively without a container (dev mode, no sandbox)")

	_ = replCmd.RegisterFlagCompletionFunc("group", func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return nil, cobra.ShellCompDirectiveNoFileComp
	})

	rootCmd.AddCommand(replCmd)
}

// turn holds one round-trip in the conversation history buffer.
type turn struct {
	user  string
	agent string
}

func runRepl(cmd *cobra.Command, _ []string) error {
	archName := flagArch
	if archName == "" {
		archName = "nanoclaw"
	}

	d, err := locateDriver(archName)
	if err != nil {
		return err
	}

	sessionID := flagSession

	group := flagGroup
	if group == "" {
		group = "main"
	}
	fmt.Fprintf(os.Stderr, "claw repl — %s / %s", d.Arch, group)
	if sessionID != "" {
		fmt.Fprintf(os.Stderr, " (session: %s)", sessionID)
	}
	fmt.Fprintln(os.Stderr, "\nType your prompt and press Enter. Ctrl-D or /exit to quit.\n")

	stdin := bufio.NewReader(os.Stdin)
	// TODO: cap history to avoid unbounded prompt growth on long sessions.
	var history []turn

	for {
		fmt.Fprint(os.Stderr, ">>> ")
		line, err := stdin.ReadString('\n')
		if err != nil {
			fmt.Fprintln(os.Stderr, "\nBye.")
			return nil
		}

		prompt := strings.TrimSpace(line)
		if prompt == "" {
			continue
		}

		// Slash commands
		switch prompt {
		case "/exit", "/quit", "exit", "quit":
			fmt.Fprintln(os.Stderr, "Bye.")
			return nil
		case "/session":
			if sessionID == "" {
				fmt.Fprintln(os.Stderr, "(no session)")
			} else {
				fmt.Fprintf(os.Stderr, "session: %s\n", sessionID)
			}
			continue
		case "/new":
			sessionID = ""
			history = nil
			fmt.Fprintln(os.Stderr, "(conversation cleared)")
			continue
		case "/history":
			if len(history) == 0 {
				fmt.Fprintln(os.Stderr, "(no history yet)")
			}
			for i, t := range history {
				fmt.Fprintf(os.Stderr, "[%d] You: %s\n    Agent: %s\n", i+1, t.user, t.agent)
			}
			continue
		case "/help":
			fmt.Fprintln(os.Stderr, "Commands: /exit  /quit  /new  /session  /history  /help")
			continue
		}

		// Build prompt with conversation history prefix
		fullPrompt := buildPromptWithHistory(history, prompt)

		req := map[string]interface{}{
			"type":       "agent_request",
			"source_dir": "",
			"group":      group,
			"jid":        flagJID,
			"prompt":     fullPrompt,
			"session_id": sessionID,
			"resume_at":  "",
			"native":     flagNative,
		}
		if sessionID != "" {
			req["resume_at"] = "latest"
		}

		scanner, wait, err := d.SendRequestAndClose(req)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			continue
		}

		fmt.Println()
		var responseBuf strings.Builder
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
					responseBuf.WriteString(text)
				} else {
					fmt.Println(text)
					responseBuf.WriteString(text)
				}
			case "agent_complete":
				newSessionID, _ := msg["session_id"].(string)
				if newSessionID != "" && newSessionID != sessionID {
					sessionID = newSessionID
					fmt.Fprintf(os.Stderr, "\n[session: %s]\n", sessionID)
				}
				status, _ := msg["status"].(string)
				if status != "success" && status != "" {
					errMsg, _ := msg["message"].(string)
					if errMsg != "" {
						fmt.Fprintf(os.Stderr, "error: %s\n", errMsg)
					}
				}
			case "error":
				code, _ := msg["code"].(string)
				errMsg, _ := msg["message"].(string)
				fmt.Fprintf(os.Stderr, "error [%s]: %s\n", code, errMsg)
			}
		}
		fmt.Println()

		// Record this turn in the history buffer
		response := strings.TrimSpace(responseBuf.String())
		if response != "" {
			history = append(history, turn{user: prompt, agent: response})
		}

		if err := wait(); err != nil {
			fmt.Fprintf(os.Stderr, "driver error: %v\n", err)
		}
	}
}

// buildPromptWithHistory prepends conversation history to the current prompt.
// The format is simple plain text that any LLM understands.
func buildPromptWithHistory(history []turn, current string) string {
	if len(history) == 0 {
		return current
	}

	var b strings.Builder
	b.WriteString("The following is our conversation so far:\n\n")
	for _, t := range history {
		fmt.Fprintf(&b, "User: %s\nAssistant: %s\n\n", t.user, t.agent)
	}
	b.WriteString("User: ")
	b.WriteString(current)
	return b.String()
}
