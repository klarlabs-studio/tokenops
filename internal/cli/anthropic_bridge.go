package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"go.klarlabs.de/tokenops/internal/proxy/attributionbridge"
)

func newAnthropicBridgeCmd() *cobra.Command {
	var executionID string
	var proxyURL string
	cmd := &cobra.Command{
		Use:   "anthropic-bridge -- <command> [args...]",
		Short: "Launch a client through TokenOps with a stable execution ID",
		Long: `Launch one client attempt through the local TokenOps Anthropic proxy.

The child process receives an ephemeral local ANTHROPIC_BASE_URL. The bridge
adds one execution ID to every Anthropic request and forwards credentials and
request content unchanged. TokenOps strips the correlation ID before sending
to Anthropic. This does not change other running clients or enroll a trial.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(executionID) == "" {
				executionID = "claude:" + uuid.NewString()
			}
			handler, err := attributionbridge.NewHandler(proxyURL, executionID)
			if err != nil {
				return err
			}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				return fmt.Errorf("anthropic bridge: listen: %w", err)
			}
			server := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
			serveErr := make(chan error, 1)
			go func() {
				if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
					serveErr <- err
				}
			}()
			defer func() {
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				_ = server.Shutdown(shutdownCtx)
			}()

			baseURL := "http://" + listener.Addr().String() + "/anthropic"
			child := exec.CommandContext(cmd.Context(), args[0], args[1:]...)
			child.Stdin, child.Stdout, child.Stderr = cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr()
			child.Env = replaceEnvironment(os.Environ(), "ANTHROPIC_BASE_URL", baseURL)
			fmt.Fprintf(cmd.ErrOrStderr(), "TokenOps execution ID: %s\n", executionID)
			err = child.Run()
			select {
			case serveErr := <-serveErr:
				if serveErr != nil {
					return fmt.Errorf("anthropic bridge: serve: %w", serveErr)
				}
			default:
			}
			return err
		},
	}
	cmd.Flags().StringVar(&executionID, "execution-id", "", "stable ID for this work attempt (generated if omitted)")
	cmd.Flags().StringVar(&proxyURL, "proxy-url", "http://127.0.0.1:7878", "local TokenOps proxy base URL")
	return cmd
}

func replaceEnvironment(env []string, name, value string) []string {
	prefix := name + "="
	out := make([]string, 0, len(env)+1)
	for _, item := range env {
		if !strings.HasPrefix(item, prefix) {
			out = append(out, item)
		}
	}
	return append(out, prefix+value)
}
