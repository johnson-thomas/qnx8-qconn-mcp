package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type execIn struct {
	Command string `json:"command" jsonschema:"shell command to run on the target via /bin/sh -c"`
}

type execOut struct {
	PID    int    `json:"pid"`
	Output string `json:"output"`
}

type runIn struct {
	Program string   `json:"program" jsonschema:"program path to launch on the target"`
	Args    []string `json:"args,omitempty" jsonschema:"command-line arguments"`
}

type runOut struct {
	PID int `json:"pid"`
}

func (s *Server) registerExecTools() {
	addTool(s, "qconn_exec",
		"Execute a shell command on the target (via the launcher service, '/bin/sh -c') and capture its combined stdout/stderr. NOTE: qconn runs commands as root with no authentication.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in execIn) (*mcp.CallToolResult, execOut, error) {
			c, err := s.cli(ctx)
			if err != nil {
				return nil, execOut{}, err
			}
			r, err := c.Exec(ctx, in.Command)
			if err != nil {
				return nil, execOut{}, err
			}
			return nil, execOut{PID: r.PID, Output: r.Output}, nil
		})

	addTool(s, "qconn_run",
		"Launch a program on the target in the background and return its PID. Use qconn_exec when you need to capture output.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in runIn) (*mcp.CallToolResult, runOut, error) {
			c, err := s.cli(ctx)
			if err != nil {
				return nil, runOut{}, err
			}
			pid, err := c.Run(ctx, in.Program, in.Args...)
			if err != nil {
				return nil, runOut{}, err
			}
			return nil, runOut{PID: pid}, nil
		})
}
