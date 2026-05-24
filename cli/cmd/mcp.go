package cmd

import (
	"context"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/ranaroussi/minigun/cli/internal/mcp"
)

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Run a Model Context Protocol server over stdio",
	Long: `Expose MiniGun to MCP-aware clients (Claude Desktop, Cursor, etc.) over stdio.

The server reuses the CLI's --api and --token flags (and their env fallbacks
MINIGUN_API_URL / MINIGUN_API_TOKEN), so you can configure it once and reuse
it across both modes.`,
	RunE: runMCP,
}

func init() {
	rootCmd.AddCommand(mcpCmd)
}

func runMCP(cmd *cobra.Command, args []string) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return mcp.Run(ctx, newClient())
}
