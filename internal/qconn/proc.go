package qconn

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// ReadMemory reads len bytes from a process's virtual address space by reading
// /proc/<pid>/as at the virtual address offset. On QNX, /proc/<pid>/as is the
// process address space; seeking to a virtual address and reading returns the
// memory at that address. Requires that qconn (root) can open the node.
func (c *Client) ReadMemory(ctx context.Context, pid int, vaddr uint64, n int) ([]byte, error) {
	path := fmt.Sprintf("/proc/%d/as", pid)
	return c.ReadFileRange(ctx, path, int64(vaddr), n)
}

// WriteMemory writes data into a process's virtual address space via
// /proc/<pid>/as at the given virtual address.
func (c *Client) WriteMemory(ctx context.Context, pid int, vaddr uint64, data []byte) (int, error) {
	path := fmt.Sprintf("/proc/%d/as", pid)
	return c.WriteFileAt(ctx, path, int64(vaddr), data)
}

// MapSegment is one mapping in a process's virtual address space.
type MapSegment struct {
	Start  uint64 `json:"start"`
	End    uint64 `json:"end"`
	Size   uint64 `json:"size"`
	Flags  string `json:"flags,omitempty"`
	Object string `json:"object,omitempty"`
}

// MemoryMap returns the virtual-address-space map of a process. It is derived
// from `pidin -p <pid> mem`/`pmem`, which lists segment ranges; the raw text is
// returned alongside for ground truth.
func (c *Client) MemoryMap(ctx context.Context, pid int) ([]MapSegment, string, error) {
	r, err := c.Exec(ctx, fmt.Sprintf("pidin -p %d mem", pid))
	if err != nil {
		return nil, "", err
	}
	return parseMapSegments(r.Output), r.Output, nil
}

// parseMapSegments extracts mappings from `pidin -p <pid> mem` output. The QNX 8
// format lists one mapping per line as: "<object> @<hexbase> <code> <data>"
// (e.g. "libc.so.6 @5313b75000 672K 8192"). Older/range forms ("START-END") are
// also accepted as a fallback.
func parseMapSegments(out string) []MapSegment {
	var segs []MapSegment
	for _, ln := range splitLines(out) {
		f := strings.Fields(ln)
		// "@<hexbase>" base-address form.
		matched := false
		for i, tok := range f {
			if !strings.HasPrefix(tok, "@") {
				continue
			}
			if addr, err := parseHexAddr(strings.TrimPrefix(tok, "@")); err == nil {
				seg := MapSegment{Start: addr}
				if i > 0 {
					seg.Object = f[0]
				}
				if i+1 < len(f) {
					seg.Flags = strings.Join(f[i+1:], " ") // code/data sizes
				}
				segs = append(segs, seg)
				matched = true
			}
			break
		}
		if matched {
			continue
		}
		// Fallback: "START-END" hex range form.
		for _, tok := range f {
			lo, hi, ok := splitHexRange(tok)
			if !ok {
				continue
			}
			seg := MapSegment{Start: lo, End: hi}
			if hi >= lo {
				seg.Size = hi - lo
			}
			if last := f[len(f)-1]; strings.HasPrefix(last, "/") {
				seg.Object = last
			}
			segs = append(segs, seg)
			break
		}
	}
	return segs
}

// splitHexRange parses "START-END" where both sides are hex (optional 0x).
func splitHexRange(tok string) (uint64, uint64, bool) {
	i := strings.IndexByte(tok, '-')
	if i <= 0 || i >= len(tok)-1 {
		return 0, 0, false
	}
	lo, err1 := parseHexAddr(tok[:i])
	hi, err2 := parseHexAddr(tok[i+1:])
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return lo, hi, true
}

func parseHexAddr(s string) (uint64, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "0x")
	s = strings.TrimPrefix(s, "0X")
	if s == "" {
		return 0, strconv.ErrSyntax
	}
	return strconv.ParseUint(s, 16, 64)
}
