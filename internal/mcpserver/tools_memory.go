package mcpserver

import (
	"context"
	"encoding/base64"
	"encoding/hex"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/johnson-thomas/qnx8-qconn-mcp/internal/qconn"
)

type memMapOut struct {
	Segments []qconn.MapSegment `json:"segments"`
	Raw      string             `json:"raw"`
}

type readMemIn struct {
	PID    int    `json:"pid" jsonschema:"target process id"`
	Addr   uint64 `json:"addr" jsonschema:"virtual address to read from"`
	Length int    `json:"length" jsonschema:"number of bytes to read"`
}

type readMemOut struct {
	Addr   uint64 `json:"addr"`
	Length int    `json:"length"`
	Hex    string `json:"hex"`
	Base64 string `json:"base64"`
}

type writeMemIn struct {
	PID     int    `json:"pid" jsonschema:"target process id"`
	Addr    uint64 `json:"addr" jsonschema:"virtual address to write to"`
	HexData string `json:"hex_data" jsonschema:"bytes to write, as a hex string (e.g. 'deadbeef')"`
}

type writeMemOut struct {
	Written int `json:"written"`
}

func (s *Server) registerMemoryTools() {
	addTool(s, "qconn_memory_map",
		"Return a process's virtual address-space map (segment ranges) derived from pidin memory output.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in pidIn) (*mcp.CallToolResult, memMapOut, error) {
			c, err := s.cli(ctx)
			if err != nil {
				return nil, memMapOut{}, err
			}
			segs, raw, err := c.MemoryMap(ctx, in.PID)
			if err != nil {
				return nil, memMapOut{}, err
			}
			return nil, memMapOut{Segments: segs, Raw: raw}, nil
		})

	addTool(s, "qconn_read_memory",
		"Read raw bytes from a process's virtual memory via /proc/<pid>/as. Returns hex and base64. Requires qconn root access on the target.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in readMemIn) (*mcp.CallToolResult, readMemOut, error) {
			c, err := s.cli(ctx)
			if err != nil {
				return nil, readMemOut{}, err
			}
			b, err := c.ReadMemory(ctx, in.PID, in.Addr, in.Length)
			if err != nil {
				return nil, readMemOut{}, err
			}
			return nil, readMemOut{
				Addr:   in.Addr,
				Length: len(b),
				Hex:    hex.EncodeToString(b),
				Base64: base64.StdEncoding.EncodeToString(b),
			}, nil
		})

	addTool(s, "qconn_write_memory",
		"Write raw bytes (given as hex) into a process's virtual memory via /proc/<pid>/as. Dangerous: can corrupt the target process.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in writeMemIn) (*mcp.CallToolResult, writeMemOut, error) {
			data, err := hex.DecodeString(in.HexData)
			if err != nil {
				return fail2[writeMemOut]("invalid hex_data: %v", err)
			}
			c, err := s.cli(ctx)
			if err != nil {
				return nil, writeMemOut{}, err
			}
			n, err := c.WriteMemory(ctx, in.PID, in.Addr, data)
			if err != nil {
				return nil, writeMemOut{}, err
			}
			return nil, writeMemOut{Written: n}, nil
		})
}

// fail2 returns a typed error result for tools with a non-empty Out type.
func fail2[Out any](format string, a ...any) (*mcp.CallToolResult, Out, error) {
	var zero Out
	r, _ := fail(format, a...)
	return r, zero, nil
}
