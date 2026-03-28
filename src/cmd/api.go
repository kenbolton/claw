// SPDX-License-Identifier: AGPL-3.0-or-later
package cmd

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kenbolton/claw/api"
	"github.com/kenbolton/claw/console"
	"github.com/kenbolton/claw/driver"
	"github.com/spf13/cobra"
)

var (
	flagAPIPort      int
	flagAPIBind      string
	flagAPIToken     string
	flagAPISourceDir string
	flagAPICORS      []string
	flagAPIConsole   bool
)

var apiCmd = &cobra.Command{
	Use:   "api",
	Short: "HTTP+WebSocket API server for claw-console",
	Long: `Start and manage the claw API server.

The API server exposes the driver protocol over HTTP and WebSocket
for use by claw-console and other HTTP clients.

Examples:
  claw api serve
  claw api serve --port 8080
  claw api serve --bind 0.0.0.0 --token mysecret`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

var apiServeCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the API server",
	Long: `Start the HTTP+WebSocket API server.

Binds 127.0.0.1 by default. Attempting --bind 0.0.0.0 without --token
is a hard error — no silent remote exposure.

Examples:
  claw api serve                           # localhost:7474
  claw api serve --console                 # serve dashboard on same port
  claw api serve --port 8080               # custom port
  claw api serve --bind 0.0.0.0 --token s  # expose with auth
  claw api serve --arch nanoclaw           # one architecture only`,
	RunE: runAPIServe,
}

func init() {
	apiServeCmd.Flags().IntVar(&flagAPIPort, "port", 7474, "Port to listen on")
	apiServeCmd.Flags().StringVar(&flagAPIBind, "bind", "127.0.0.1", "Address to bind to")
	apiServeCmd.Flags().StringVar(&flagAPIToken, "token", "", "Bearer token for authentication")
	apiServeCmd.Flags().StringVar(&flagAPISourceDir, "source-dir", "", "Target a specific installation directory")
	apiServeCmd.Flags().StringSliceVar(&flagAPICORS, "cors-origin", nil, "Additional allowed CORS origins")
	apiServeCmd.Flags().BoolVar(&flagAPIConsole, "console", false, "Serve claw-console dashboard from the same port")

	apiCmd.AddCommand(apiServeCmd)
	rootCmd.AddCommand(apiCmd)
}

func runAPIServe(cmd *cobra.Command, args []string) error {
	// Validate: non-localhost bind requires token.
	if flagAPIBind != "127.0.0.1" && flagAPIBind != "localhost" && flagAPIBind != "::1" && flagAPIToken == "" {
		return fmt.Errorf("--token is required when binding to %s (non-localhost)", flagAPIBind)
	}

	// Resolve drivers.
	var drivers []*driver.Driver
	if flagArch != "" {
		d, err := locateDriver(flagArch, flagAPISourceDir)
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
			return fmt.Errorf("no drivers installed — run 'claw archs' for info")
		}
	}

	srv := &api.Server{
		Drivers:     drivers,
		SourceDir:   flagAPISourceDir,
		Token:       flagAPIToken,
		Bind:        flagAPIBind,
		Port:        flagAPIPort,
		CORSOrigins: flagAPICORS,
	}

	if flagAPIConsole {
		sub, err := fs.Sub(console.Assets, "dist")
		if err != nil {
			return fmt.Errorf("failed to load embedded console: %w", err)
		}
		srv.ConsoleFS = sub
	}

	// Graceful shutdown on SIGINT/SIGTERM.
	httpServer := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", flagAPIBind, flagAPIPort),
		Handler: srv.NewServeMux(),
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigCh
		fmt.Println("\nShutting down...")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(ctx)
	}()

	authStatus := "off"
	if flagAPIToken != "" {
		authStatus = "on"
	}
	consoleStatus := "off"
	if flagAPIConsole {
		consoleStatus = "on"
	}
	fmt.Printf("claw api serve — listening on %s:%d (%d drivers, auth: %s, console: %s)\n",
		flagAPIBind, flagAPIPort, len(drivers), authStatus, consoleStatus)

	err := httpServer.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}
