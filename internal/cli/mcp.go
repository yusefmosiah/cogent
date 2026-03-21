package cli

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
	"github.com/yusefmosiah/fase/internal/mcpserver"
	"github.com/yusefmosiah/fase/internal/service"
)

func newMCPCommand(root *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Run fase as an MCP server",
	}

	stdioCmd := &cobra.Command{
		Use:   "stdio",
		Short: "Run MCP server over stdio (for Claude Code and other MCP clients)",
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := service.Open(context.Background(), root.configPath)
			if err != nil {
				return err
			}
			defer func() { _ = svc.Close() }()

			server := mcpserver.New(svc)
			return server.RunStdio(cmd.Context())
		},
	}

	var httpAddr string
	httpCmd := &cobra.Command{
		Use:   "http",
		Short: "Run MCP server over HTTP streaming",
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := service.Open(context.Background(), root.configPath)
			if err != nil {
				return err
			}
			defer func() { _ = svc.Close() }()

			server := mcpserver.New(svc)
			handler := mcp.NewStreamableHTTPHandler(func(_ *http.Request) *mcp.Server {
				return server.MCP
			}, nil)

			fmt.Fprintf(cmd.OutOrStdout(), "FASE MCP server listening on %s\n", httpAddr)
			return http.ListenAndServe(httpAddr, handler)
		},
	}
	httpCmd.Flags().StringVar(&httpAddr, "addr", ":4243", "HTTP listen address")

	proxyCmd := &cobra.Command{
		Use:   "proxy",
		Short: "Proxy MCP stdio to the running fase serve HTTP endpoint",
		Long: `Reads serve.json to find the running fase serve port, then proxies
MCP requests from stdin to serve's /mcp endpoint over HTTP. This avoids
the WAL split problem where a separate DB connection sees stale data.

Use this in .mcp.json instead of 'fase mcp stdio'.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			info, err := loadServeInfo()
			if err != nil {
				return fmt.Errorf("fase serve is not running: %w", err)
			}
			baseURL := fmt.Sprintf("http://localhost:%d/mcp", info.Port)
			return runMCPProxy(cmd.Context(), baseURL)
		},
	}

	cmd.AddCommand(stdioCmd, httpCmd, proxyCmd)
	return cmd
}

// runMCPProxy proxies MCP JSON-RPC from stdin/stdout to an HTTP endpoint.
// Each line from stdin is POSTed to the endpoint; the response is written to stdout.
func runMCPProxy(ctx context.Context, endpoint string) error {
	client := &http.Client{}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for scanner.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(line))
		if err != nil {
			return fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")

		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("proxy request: %w", err)
		}

		if _, err := io.Copy(os.Stdout, resp.Body); err != nil {
			resp.Body.Close()
			return fmt.Errorf("proxy response: %w", err)
		}
		resp.Body.Close()

		// MCP streamable HTTP may not end with newline
		fmt.Fprintln(os.Stdout)
	}
	return scanner.Err()
}
