package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type signalIn struct {
	PID    int `json:"pid" jsonschema:"target process id"`
	Signal int `json:"signal" jsonschema:"QNX signal number (e.g. 15=SIGTERM, 9=SIGKILL, 17=SIGSTOP, 19=SIGCONT)"`
}

type okOut struct {
	OK bool `json:"ok"`
}

func (s *Server) registerControlTools() {
	addTool(s, "qconn_signal",
		"Send a signal to a target process via the cntl service (e.g. SIGTERM/SIGKILL/SIGSTOP/SIGCONT). QNX signal numbers differ from Linux for STOP/CONT.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in signalIn) (*mcp.CallToolResult, okOut, error) {
			c, err := s.cli(ctx)
			if err != nil {
				return nil, okOut{}, err
			}
			if err := c.Signal(ctx, in.PID, in.Signal); err != nil {
				return nil, okOut{}, err
			}
			return nil, okOut{OK: true}, nil
		})

	addTool(s, "qconn_kill",
		"Terminate a target process (sends SIGKILL).",
		func(ctx context.Context, _ *mcp.CallToolRequest, in pidIn) (*mcp.CallToolResult, okOut, error) {
			c, err := s.cli(ctx)
			if err != nil {
				return nil, okOut{}, err
			}
			if err := c.Kill(ctx, in.PID); err != nil {
				return nil, okOut{}, err
			}
			return nil, okOut{OK: true}, nil
		})
}
