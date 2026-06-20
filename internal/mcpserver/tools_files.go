package mcpserver

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type readFileIn struct {
	Path string `json:"path" jsonschema:"absolute path on the target"`
}

type readFileOut struct {
	Path   string `json:"path"`
	Size   int    `json:"size"`
	Base64 string `json:"base64"`
	Text   string `json:"text,omitempty"`
}

type writeFileIn struct {
	Path   string `json:"path" jsonschema:"absolute path on the target"`
	Base64 string `json:"base64,omitempty" jsonschema:"file content as base64 (takes precedence over text)"`
	Text   string `json:"text,omitempty" jsonschema:"file content as UTF-8 text"`
	Mode   int    `json:"mode,omitempty" jsonschema:"octal-derived permission bits (default 0644)"`
}

type writeFileOut struct {
	Written int `json:"written"`
}

type pathIn struct {
	Path string `json:"path" jsonschema:"absolute path on the target"`
}

type statOut struct {
	Path string `json:"path"`
	Mode int64  `json:"mode"`
	Size int64  `json:"size"`
}

type listDirOut struct {
	Path    string   `json:"path"`
	Entries []string `json:"entries"`
	Raw     string   `json:"raw"`
}

type mkdirIn struct {
	Path string `json:"path" jsonschema:"directory path to create on the target"`
	Mode int    `json:"mode,omitempty" jsonschema:"permission bits (default 0777)"`
}

type chmodIn struct {
	Path string `json:"path" jsonschema:"path on the target"`
	Mode int    `json:"mode" jsonschema:"permission bits to set"`
}

func (s *Server) registerFileTools() {
	addTool(s, "qconn_read_file",
		"Read a file from the target via the file service. Returns base64 always, and UTF-8 text when the content is printable.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in readFileIn) (*mcp.CallToolResult, readFileOut, error) {
			c, err := s.cli(ctx)
			if err != nil {
				return nil, readFileOut{}, err
			}
			b, err := c.ReadFile(ctx, in.Path)
			if err != nil {
				return nil, readFileOut{}, err
			}
			out := readFileOut{Path: in.Path, Size: len(b), Base64: base64.StdEncoding.EncodeToString(b)}
			if isPrintable(b) {
				out.Text = string(b)
			}
			return nil, out, nil
		})

	addTool(s, "qconn_write_file",
		"Create/overwrite a file on the target via the file service. Provide content as base64 or text.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in writeFileIn) (*mcp.CallToolResult, writeFileOut, error) {
			var data []byte
			switch {
			case in.Base64 != "":
				d, err := base64.StdEncoding.DecodeString(in.Base64)
				if err != nil {
					return fail2[writeFileOut]("invalid base64: %v", err)
				}
				data = d
			default:
				data = []byte(in.Text)
			}
			c, err := s.cli(ctx)
			if err != nil {
				return nil, writeFileOut{}, err
			}
			n, err := c.WriteFile(ctx, in.Path, data, in.Mode)
			if err != nil {
				return nil, writeFileOut{}, err
			}
			return nil, writeFileOut{Written: n}, nil
		})

	addTool(s, "qconn_stat",
		"Stat a target path (mode and size) via the file service.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in pathIn) (*mcp.CallToolResult, statOut, error) {
			c, err := s.cli(ctx)
			if err != nil {
				return nil, statOut{}, err
			}
			mode, size, err := c.Stat(ctx, in.Path)
			if err != nil {
				return nil, statOut{}, err
			}
			return nil, statOut{Path: in.Path, Mode: mode, Size: size}, nil
		})

	addTool(s, "qconn_list_dir",
		"List a directory on the target (via 'ls -la' through the launcher).",
		func(ctx context.Context, _ *mcp.CallToolRequest, in pathIn) (*mcp.CallToolResult, listDirOut, error) {
			c, err := s.cli(ctx)
			if err != nil {
				return nil, listDirOut{}, err
			}
			r, err := c.Exec(ctx, fmt.Sprintf("ls -la %q", in.Path))
			if err != nil {
				return nil, listDirOut{}, err
			}
			var entries []string
			for _, ln := range strings.Split(r.Output, "\n") {
				if ln = strings.TrimRight(ln, "\r"); strings.TrimSpace(ln) != "" {
					entries = append(entries, ln)
				}
			}
			return nil, listDirOut{Path: in.Path, Entries: entries, Raw: r.Output}, nil
		})

	addTool(s, "qconn_delete",
		"Delete a file or directory on the target via the file service.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in pathIn) (*mcp.CallToolResult, okOut, error) {
			c, err := s.cli(ctx)
			if err != nil {
				return nil, okOut{}, err
			}
			if err := c.Delete(ctx, in.Path); err != nil {
				return nil, okOut{}, err
			}
			return nil, okOut{OK: true}, nil
		})

	addTool(s, "qconn_mkdir",
		"Create a directory on the target via the file service.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in mkdirIn) (*mcp.CallToolResult, okOut, error) {
			c, err := s.cli(ctx)
			if err != nil {
				return nil, okOut{}, err
			}
			if err := c.Mkdir(ctx, in.Path, in.Mode); err != nil {
				return nil, okOut{}, err
			}
			return nil, okOut{OK: true}, nil
		})

	addTool(s, "qconn_chmod",
		"Change permission bits of a target path via the file service.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in chmodIn) (*mcp.CallToolResult, okOut, error) {
			c, err := s.cli(ctx)
			if err != nil {
				return nil, okOut{}, err
			}
			if err := c.Chmod(ctx, in.Path, in.Mode); err != nil {
				return nil, okOut{}, err
			}
			return nil, okOut{OK: true}, nil
		})
}

// isPrintable reports whether b looks like UTF-8 text safe to inline.
func isPrintable(b []byte) bool {
	if len(b) > 1<<20 {
		return false
	}
	for _, c := range b {
		if c == 0 {
			return false
		}
		if c < 0x09 || (c > 0x0d && c < 0x20) {
			return false
		}
	}
	return true
}
