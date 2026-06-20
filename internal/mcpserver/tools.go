package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/johnson-thomas/qnx8-qconn-mcp/internal/qconn"
)

// Empty is the input type for tools that take no parameters.
type Empty struct{}

// registerTools wires every MCP tool group onto the server.
func (s *Server) registerTools() {
	s.registerSystemTools()
	s.registerProcessTools()
	s.registerMemoryTools()
	s.registerExecTools()
	s.registerControlTools()
	s.registerFileTools()
	s.registerDebugTools()
}

// cli is a convenience accessor that dials/reuses the qconn client and resets it
// on dial failure so a later call can retry.
func (s *Server) cli(ctx context.Context) (*qconn.Client, error) {
	c, err := s.client(ctx)
	if err != nil {
		s.resetClient()
		return nil, err
	}
	return c, nil
}

// addTool is a thin generic wrapper around mcp.AddTool for brevity.
func addTool[In, Out any](s *Server, name, desc string, h mcp.ToolHandlerFor[In, Out]) {
	mcp.AddTool(s.mcp, &mcp.Tool{Name: name, Description: desc}, h)
}
