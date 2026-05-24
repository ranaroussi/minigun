package mcp

import (
	"context"
	"fmt"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ranaroussi/minigun/cli/internal/client"
)

const (
	ServerName    = "minigun"
	ServerVersion = "0.1.0"
)

func Run(ctx context.Context, c *client.Client) error {
	s := mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:    ServerName,
		Version: ServerVersion,
	}, nil)

	RegisterTools(s, c)
	RegisterResources(s, c)
	RegisterPrompts(s)

	if err := s.Run(ctx, &mcpsdk.StdioTransport{}); err != nil {
		return fmt.Errorf("mcp server: %w", err)
	}
	return nil
}
