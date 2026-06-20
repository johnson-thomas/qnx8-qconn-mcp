package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/johnson-thomas/qnx8-qconn-mcp/internal/qconn"
)

type listProcessesOut struct {
	Processes []qconn.Process `json:"processes"`
	Count     int             `json:"count"`
	Raw       string          `json:"raw"`
}

type pidIn struct {
	PID int `json:"pid" jsonschema:"target process id"`
}

type processInfoOut struct {
	Process *qconn.Process `json:"process"`
	Raw     string         `json:"raw"`
}

type processMemoryOut struct {
	Usage []qconn.MemUsage `json:"usage"`
	Raw   string           `json:"raw"`
}

type resourcesIn struct {
	PID  int    `json:"pid,omitempty" jsonschema:"process id; 0 or omitted for system-wide"`
	Kind string `json:"kind" jsonschema:"pidin sub-listing: fds, signals, timers, channels, mem, irqs, env, args, sched, names"`
}

type resourcesOut struct {
	Lines []string `json:"lines"`
	Raw   string   `json:"raw"`
}

func (s *Server) registerProcessTools() {
	addTool(s, "qconn_list_processes",
		"List all processes running on the target (with threads, priority and state), parsed from pidin. Also returns the raw pidin text.",
		func(ctx context.Context, _ *mcp.CallToolRequest, _ Empty) (*mcp.CallToolResult, listProcessesOut, error) {
			c, err := s.cli(ctx)
			if err != nil {
				return nil, listProcessesOut{}, err
			}
			procs, raw, err := c.ListProcesses(ctx)
			if err != nil {
				return nil, listProcessesOut{}, err
			}
			return nil, listProcessesOut{Processes: procs, Count: len(procs), Raw: raw}, nil
		})

	addTool(s, "qconn_process_info",
		"Return detailed information for a single process (its threads, priorities and states).",
		func(ctx context.Context, _ *mcp.CallToolRequest, in pidIn) (*mcp.CallToolResult, processInfoOut, error) {
			c, err := s.cli(ctx)
			if err != nil {
				return nil, processInfoOut{}, err
			}
			p, raw, err := c.ProcessInfo(ctx, in.PID)
			if err != nil {
				return nil, processInfoOut{}, err
			}
			return nil, processInfoOut{Process: p, Raw: raw}, nil
		})

	addTool(s, "qconn_process_memory",
		"Return per-process memory usage (code/data/stack) from 'pidin mem'. Set pid=0 for all processes.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in pidIn) (*mcp.CallToolResult, processMemoryOut, error) {
			c, err := s.cli(ctx)
			if err != nil {
				return nil, processMemoryOut{}, err
			}
			u, raw, err := c.ProcessMemory(ctx, in.PID)
			if err != nil {
				return nil, processMemoryOut{}, err
			}
			return nil, processMemoryOut{Usage: u, Raw: raw}, nil
		})

	addTool(s, "qconn_resources",
		"Return a pidin resource sub-listing for a process or the whole system: file descriptors, signals, timers, channels/connections, memory segments, IRQs, environment, args, scheduling.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in resourcesIn) (*mcp.CallToolResult, resourcesOut, error) {
			c, err := s.cli(ctx)
			if err != nil {
				return nil, resourcesOut{}, err
			}
			lines, raw, err := c.Resources(ctx, in.PID, in.Kind)
			if err != nil {
				return nil, resourcesOut{}, err
			}
			return nil, resourcesOut{Lines: lines, Raw: raw}, nil
		})
}
