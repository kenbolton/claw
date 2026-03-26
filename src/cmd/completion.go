// SPDX-License-Identifier: AGPL-3.0-or-later
package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/kenbolton/claw/driver"
	"github.com/spf13/cobra"
)

var completionCmd = &cobra.Command{
	Use:       "completion <bash|zsh|fish>",
	Short:     "Generate shell completion scripts",
	ValidArgs: []string{"bash", "zsh", "fish"},
	Args:      cobra.ExactArgs(1),
	RunE:      runCompletion,
}

var completionInstall bool

func init() {
	completionCmd.Flags().BoolVar(&completionInstall, "install", false,
		"Install the completion script to the appropriate location")
	rootCmd.AddCommand(completionCmd)
}

func runCompletion(cmd *cobra.Command, args []string) error {
	shell := args[0]

	if completionInstall {
		return installCompletion(shell)
	}

	switch shell {
	case "bash":
		return rootCmd.GenBashCompletionV2(os.Stdout, true)
	case "zsh":
		return rootCmd.GenZshCompletion(os.Stdout)
	case "fish":
		return rootCmd.GenFishCompletion(os.Stdout, true)
	default:
		return fmt.Errorf("unknown shell %q: must be bash, zsh, or fish", shell)
	}
}

func shellCompletionPath(shell string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	xdgData := os.Getenv("XDG_DATA_HOME")
	if xdgData == "" {
		xdgData = filepath.Join(home, ".local", "share")
	}
	switch shell {
	case "bash":
		return filepath.Join(xdgData, "bash-completion", "completions", "claw"), nil
	case "zsh":
		return filepath.Join(home, ".zsh", "completions", "_claw"), nil
	case "fish":
		return filepath.Join(xdgData, "fish", "vendor_completions.d", "claw.fish"), nil
	default:
		return "", fmt.Errorf("unknown shell %q", shell)
	}
}

func installCompletion(shell string) error {
	path, err := shellCompletionPath(shell)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		printManualInstall(shell)
		return nil
	}

	f, err := os.Create(path)
	if err != nil {
		printManualInstall(shell)
		return nil
	}
	defer func() { _ = f.Close() }()

	switch shell {
	case "bash":
		err = rootCmd.GenBashCompletionV2(f, true)
	case "zsh":
		err = rootCmd.GenZshCompletion(f)
	case "fish":
		err = rootCmd.GenFishCompletion(f, true)
	}
	if err != nil {
		_ = os.Remove(path)
		return err
	}

	fmt.Printf("Installed to %s\n", path)
	if shell == "zsh" {
		fmt.Println(`
Add to ~/.zshrc if not already present:
  fpath=(~/.zsh/completions $fpath)
  autoload -Uz compinit && compinit`)
	}
	return nil
}

func printManualInstall(shell string) {
	switch shell {
	case "bash":
		fmt.Println("Add to ~/.bashrc:\n  source <(claw completion bash)")
	case "zsh":
		fmt.Println("Add to ~/.zshrc:\n  source <(claw completion zsh)")
	case "fish":
		fmt.Println("Run:\n  claw completion fish > ~/.config/fish/completions/claw.fish")
	}
}

// completeArchs returns installed driver arch names for --arch completion.
func completeArchs(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	drivers, err := driver.FindAll()
	if err == nil && len(drivers) > 0 {
		archs := make([]string, 0, len(drivers))
		for _, d := range drivers {
			archs = append(archs, d.Arch)
		}
		return archs, cobra.ShellCompDirectiveNoFileComp
	}
	return []string{"nanoclaw", "zepto", "openclaw", "pico"}, cobra.ShellCompDirectiveNoFileComp
}
