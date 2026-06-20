package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type systemInfoOut struct {
	Target     string            `json:"target"`
	Attributes map[string]string `json:"attributes"`
}

type listServicesOut struct {
	Services map[string]int `json:"services"`
}

type systemMemoryOut struct {
	Summary map[string]string `json:"summary"`
	Raw     string            `json:"raw"`
}

func (s *Server) registerSystemTools() {
	addTool(s, "qconn_system_info",
		"Return the QNX target's system attributes (ARCHITECTURE, CPU, OS, RELEASE, HOSTNAME, ENDIAN, TIMEZONE, ...) via the qconn 'info' command.",
		func(ctx context.Context, _ *mcp.CallToolRequest, _ Empty) (*mcp.CallToolResult, systemInfoOut, error) {
			c, err := s.cli(ctx)
			if err != nil {
				return nil, systemInfoOut{}, err
			}
			attrs, err := c.Info(ctx)
			if err != nil {
				return nil, systemInfoOut{}, err
			}
			return nil, systemInfoOut{Target: c.Target(), Attributes: attrs}, nil
		})

	addTool(s, "qconn_list_services",
		"List the qconn services advertised by the target and their protocol versions (broker, launcher, file, cntl, sinfo, ...).",
		func(ctx context.Context, _ *mcp.CallToolRequest, _ Empty) (*mcp.CallToolResult, listServicesOut, error) {
			c, err := s.cli(ctx)
			if err != nil {
				return nil, listServicesOut{}, err
			}
			v, err := c.Versions(ctx)
			if err != nil {
				return nil, listServicesOut{}, err
			}
			return nil, listServicesOut{Services: v}, nil
		})

	addTool(s, "qconn_system_memory",
		"Return the target's system-wide memory/CPU summary (via 'pidin info'), with both parsed fields and raw text.",
		func(ctx context.Context, _ *mcp.CallToolRequest, _ Empty) (*mcp.CallToolResult, systemMemoryOut, error) {
			c, err := s.cli(ctx)
			if err != nil {
				return nil, systemMemoryOut{}, err
			}
			m, raw, err := c.SystemMemory(ctx)
			if err != nil {
				return nil, systemMemoryOut{}, err
			}
			return nil, systemMemoryOut{Summary: m, Raw: raw}, nil
		})
}
