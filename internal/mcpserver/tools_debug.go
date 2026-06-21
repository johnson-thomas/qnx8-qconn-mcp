package mcpserver

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/johnson-thomas/qnx8-qconn-mcp/internal/qnxdbg"
)

var sessionSeq atomic.Int64

// qnxSession is an active pdebug debug session (QNX DSMSG protocol).
type qnxSession struct {
	mu       sync.Mutex
	client   *qnxdbg.Client
	pid      int
	sigTable []byte // DSMSG Handlesig disposition (1=intercept), lazily initialised
}

// signalTable returns the session's signal-disposition table, defaulting to
// intercept-all on first use.
func (q *qnxSession) signalTable() []byte {
	if q.sigTable == nil {
		q.sigTable = make([]byte, qnxdbg.NumSignals)
		for i := range q.sigTable {
			q.sigTable[i] = 1
		}
	}
	return q.sigTable
}

func (q *qnxSession) Close() error { return q.client.Close() }

// dialPdebug establishes a DSMSG connection to pdebug: over the configured
// serial line if set (pdebug must already be running on it), otherwise it spawns
// pdebug on the given TCP port (0 = server default) via qconn and dials it.
func (s *Server) dialPdebug(ctx context.Context, debugPort int) (*qnxdbg.Client, error) {
	if s.cfg.DebugSerial != "" {
		cl, err := qnxdbg.ConnectSerial(ctx, s.cfg.DebugSerial, s.cfg.DebugBaud, s.cfg.Logger, s.cfg.Timeout)
		if err != nil {
			return nil, fmt.Errorf("connect pdebug (serial %s): %w", s.cfg.DebugSerial, err)
		}
		return cl, nil
	}
	port := debugPort
	if port == 0 {
		port = s.cfg.DebugPort
	}
	if err := s.mgr.SpawnPdebug(ctx, port); err != nil {
		return nil, err
	}
	addr := net.JoinHostPort(s.cfg.QconnHost, strconv.Itoa(port))
	cl, err := qnxdbg.Connect(ctx, addr, s.cfg.Logger, s.cfg.Timeout)
	if err != nil {
		return nil, fmt.Errorf("connect pdebug: %w", err)
	}
	return cl, nil
}

func (s *Server) getSession(id string) (*qnxSession, error) {
	s.dbgMu.Lock()
	defer s.dbgMu.Unlock()
	sess, ok := s.sessions[id]
	if !ok {
		return nil, fmt.Errorf("unknown debug session %q", id)
	}
	return sess, nil
}

type attachIn struct {
	PID       int `json:"pid" jsonschema:"process id to attach the debugger to"`
	DebugPort int `json:"debug_port,omitempty" jsonschema:"TCP port pdebug should listen on (default from server config)"`
}

type attachOut struct {
	SessionID string `json:"session_id"`
	PID       int    `json:"pid"`
	Note      string `json:"note,omitempty"`
}

type sessionIn struct {
	SessionID string `json:"session_id" jsonschema:"debug session id from qconn_debug_attach"`
}

type launchIn struct {
	Path      string   `json:"path" jsonschema:"path ON THE TARGET of the program to launch under the debugger"`
	Args      []string `json:"args,omitempty" jsonschema:"optional arguments (argv[1:])"`
	DebugPort int      `json:"debug_port,omitempty" jsonschema:"TCP port pdebug should listen on (default from server config)"`
}

type launchOut struct {
	SessionID string `json:"session_id"`
	PID       int    `json:"pid"`
	TID       int    `json:"tid"`
	Note      string `json:"note,omitempty"`
}

type stopOut struct {
	Stop *qnxdbg.Stop `json:"stop"`
}

type regsOut struct {
	Hex       string            `json:"hex" jsonschema:"raw register block"`
	Registers map[string]string `json:"registers,omitempty" jsonschema:"decoded aarch64 registers (x0-x30, sp, pc, pstate)"`
}

type dbgMemReadIn struct {
	SessionID string `json:"session_id"`
	Addr      uint64 `json:"addr" jsonschema:"virtual address"`
	Length    int    `json:"length" jsonschema:"number of bytes"`
}

type dbgMemWriteIn struct {
	SessionID string `json:"session_id"`
	Addr      uint64 `json:"addr"`
	HexData   string `json:"hex_data" jsonschema:"bytes to write as hex"`
}

type breakIn struct {
	SessionID string `json:"session_id"`
	Addr      uint64 `json:"addr" jsonschema:"breakpoint address"`
}

type dbgRegWriteIn struct {
	SessionID string `json:"session_id"`
	Offset    int    `json:"offset,omitempty" jsonschema:"register-area byte offset (0 = start of the general register block)"`
	HexData   string `json:"hex_data" jsonschema:"register bytes to write as hex"`
}

type selectThreadIn struct {
	SessionID string `json:"session_id"`
	TID       int    `json:"tid" jsonschema:"thread id to make current"`
}

type rawInfoOut struct {
	Hex     string   `json:"hex"`
	Strings []string `json:"strings,omitempty"`
}

type mapInfoOut struct {
	Segments []qnxdbg.MapSegment `json:"segments"`
	Hex      string              `json:"hex"`
}

type handleSigIn struct {
	SessionID string `json:"session_id"`
	Signal    int    `json:"signal" jsonschema:"QNX signal number (1-72)"`
	Stop      bool   `json:"stop" jsonschema:"true = debugger stops on this signal; false = pass it through to the process"`
}

type threadsOut struct {
	PID            int      `json:"pid"`
	Name           string   `json:"name,omitempty"`
	ThreadNames    []string `json:"thread_names,omitempty"`
	ProcInfoHex    string   `json:"proc_info_hex,omitempty"`
	ThreadNamesHex string   `json:"thread_names_hex,omitempty"`
}

func (s *Server) registerDebugTools() {
	addTool(s, "qconn_debug_attach",
		"Start pdebug on the target (via qconn) and attach the debugger to a running process using the QNX DSMSG protocol. Returns a session id for the other debug tools.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in attachIn) (*mcp.CallToolResult, attachOut, error) {
			if _, err := s.cli(ctx); err != nil { // ensures qconn + s.mgr
				return nil, attachOut{}, err
			}
			cl, err := s.dialPdebug(ctx, in.DebugPort)
			if err != nil {
				return nil, attachOut{}, err
			}
			if err := cl.Attach(ctx, in.PID); err != nil {
				cl.Close()
				return nil, attachOut{}, err
			}
			_ = cl.Select(ctx, in.PID, 1) // select main thread
			id := fmt.Sprintf("dbg-%d", sessionSeq.Add(1))
			s.dbgMu.Lock()
			s.sessions[id] = &qnxSession{client: cl, pid: in.PID}
			s.dbgMu.Unlock()
			return nil, attachOut{SessionID: id, PID: in.PID}, nil
		})

	addTool(s, "qconn_debug_launch",
		"Launch a program on the target UNDER the debugger (QNX DSMSG Load), stopped before its first instruction — the basis for debugging from main. path is the path ON THE TARGET. Returns a session id (process stopped at entry); set a breakpoint at main and call qconn_debug_continue to reach it.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in launchIn) (*mcp.CallToolResult, launchOut, error) {
			if _, err := s.cli(ctx); err != nil {
				return nil, launchOut{}, err
			}
			cl, err := s.dialPdebug(ctx, in.DebugPort)
			if err != nil {
				return nil, launchOut{}, err
			}
			res, err := cl.Launch(ctx, in.Path, in.Args, nil)
			if err != nil {
				cl.Close()
				return nil, launchOut{}, err
			}
			_ = cl.Select(ctx, res.PID, res.TID)
			id := fmt.Sprintf("dbg-%d", sessionSeq.Add(1))
			s.dbgMu.Lock()
			s.sessions[id] = &qnxSession{client: cl, pid: res.PID}
			s.dbgMu.Unlock()
			return nil, launchOut{SessionID: id, PID: res.PID, TID: res.TID,
				Note: "process is stopped at its entry point; set a breakpoint at main and qconn_debug_continue"}, nil
		})

	addTool(s, "qconn_debug_read_registers",
		"Read the aarch64 register set of the debugged process: raw hex plus decoded x0-x30, sp, pc, pstate.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in sessionIn) (*mcp.CallToolResult, regsOut, error) {
			sess, err := s.getSession(in.SessionID)
			if err != nil {
				return nil, regsOut{}, err
			}
			sess.mu.Lock()
			defer sess.mu.Unlock()
			b, err := sess.client.ReadRegisters(ctx, 0, 0x110)
			if err != nil {
				return nil, regsOut{}, err
			}
			return nil, regsOut{Hex: hex.EncodeToString(b), Registers: decodeAArch64(b)}, nil
		})

	addTool(s, "qconn_debug_read_memory",
		"Read memory from the debugged process via pdebug (DSMSG memrd).",
		func(ctx context.Context, _ *mcp.CallToolRequest, in dbgMemReadIn) (*mcp.CallToolResult, dbgReadMemOut, error) {
			sess, err := s.getSession(in.SessionID)
			if err != nil {
				return nil, dbgReadMemOut{}, err
			}
			sess.mu.Lock()
			defer sess.mu.Unlock()
			b, err := sess.client.ReadMemory(ctx, in.Addr, in.Length)
			if err != nil {
				return nil, dbgReadMemOut{}, err
			}
			return nil, dbgReadMemOut{Hex: hex.EncodeToString(b)}, nil
		})

	addTool(s, "qconn_debug_write_memory",
		"Write memory in the debugged process via pdebug (DSMSG memwr).",
		func(ctx context.Context, _ *mcp.CallToolRequest, in dbgMemWriteIn) (*mcp.CallToolResult, okOut, error) {
			data, err := hex.DecodeString(in.HexData)
			if err != nil {
				return fail2[okOut]("invalid hex_data: %v", err)
			}
			sess, err := s.getSession(in.SessionID)
			if err != nil {
				return nil, okOut{}, err
			}
			sess.mu.Lock()
			defer sess.mu.Unlock()
			if _, err := sess.client.WriteMemory(ctx, in.Addr, data); err != nil {
				return nil, okOut{}, err
			}
			return nil, okOut{OK: true}, nil
		})

	addTool(s, "qconn_debug_continue",
		"Resume the debugged process and wait for the next stop event (breakpoint/signal). Blocks until the process stops.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in sessionIn) (*mcp.CallToolResult, stopOut, error) {
			sess, err := s.getSession(in.SessionID)
			if err != nil {
				return nil, stopOut{}, err
			}
			sess.mu.Lock()
			defer sess.mu.Unlock()
			st, err := sess.client.Continue(ctx)
			if err != nil {
				return nil, stopOut{}, err
			}
			return nil, stopOut{Stop: st}, nil
		})

	addTool(s, "qconn_debug_step",
		"Single-step one instruction in the debugged process.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in sessionIn) (*mcp.CallToolResult, stopOut, error) {
			sess, err := s.getSession(in.SessionID)
			if err != nil {
				return nil, stopOut{}, err
			}
			sess.mu.Lock()
			defer sess.mu.Unlock()
			st, err := sess.client.Step(ctx, 1)
			if err != nil {
				return nil, stopOut{}, err
			}
			return nil, stopOut{Stop: st}, nil
		})

	addTool(s, "qconn_debug_break_set",
		"Insert an execution breakpoint at an address in the debugged process (QNX DSMSG Brk). Verified against real QNX 8 pdebug.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in breakIn) (*mcp.CallToolResult, okOut, error) {
			sess, err := s.getSession(in.SessionID)
			if err != nil {
				return nil, okOut{}, err
			}
			sess.mu.Lock()
			defer sess.mu.Unlock()
			if err := sess.client.SetBreakpoint(ctx, in.Addr, 0); err != nil {
				return nil, okOut{}, err
			}
			return nil, okOut{OK: true}, nil
		})

	addTool(s, "qconn_debug_break_clear",
		"Remove a breakpoint at an address in the debugged process.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in breakIn) (*mcp.CallToolResult, okOut, error) {
			sess, err := s.getSession(in.SessionID)
			if err != nil {
				return nil, okOut{}, err
			}
			sess.mu.Lock()
			defer sess.mu.Unlock()
			if err := sess.client.ClearBreakpoint(ctx, in.Addr); err != nil {
				return nil, okOut{}, err
			}
			return nil, okOut{OK: true}, nil
		})

	addTool(s, "qconn_debug_write_registers",
		"Write the register area of the debugged process (QNX DSMSG Regwr): hex bytes placed at the given register-area offset (0 = start of the general register block).",
		func(ctx context.Context, _ *mcp.CallToolRequest, in dbgRegWriteIn) (*mcp.CallToolResult, okOut, error) {
			data, err := hex.DecodeString(in.HexData)
			if err != nil {
				return fail2[okOut]("invalid hex_data: %v", err)
			}
			sess, err := s.getSession(in.SessionID)
			if err != nil {
				return nil, okOut{}, err
			}
			sess.mu.Lock()
			defer sess.mu.Unlock()
			if err := sess.client.WriteRegisters(ctx, in.Offset, data); err != nil {
				return nil, okOut{}, err
			}
			return nil, okOut{OK: true}, nil
		})

	addTool(s, "qconn_debug_select_thread",
		"Set the current thread (tid) context for subsequent debug operations (QNX DSMSG Select).",
		func(ctx context.Context, _ *mcp.CallToolRequest, in selectThreadIn) (*mcp.CallToolResult, okOut, error) {
			sess, err := s.getSession(in.SessionID)
			if err != nil {
				return nil, okOut{}, err
			}
			sess.mu.Lock()
			defer sess.mu.Unlock()
			if err := sess.client.Select(ctx, sess.pid, in.TID); err != nil {
				return nil, okOut{}, err
			}
			return nil, okOut{OK: true}, nil
		})

	addTool(s, "qconn_debug_mapinfo",
		"Return the memory map of the debugged process (QNX DSMSG): the loadable segments with their virtual load address and size, plus the raw block. Useful to find the load base of a position-independent executable so you can compute a runtime breakpoint address (base + symbol offset).",
		func(ctx context.Context, _ *mcp.CallToolRequest, in sessionIn) (*mcp.CallToolResult, mapInfoOut, error) {
			sess, err := s.getSession(in.SessionID)
			if err != nil {
				return nil, mapInfoOut{}, err
			}
			sess.mu.Lock()
			defer sess.mu.Unlock()
			raw, err := sess.client.MapInfo(ctx, sess.pid)
			if err != nil {
				return nil, mapInfoOut{}, err
			}
			return nil, mapInfoOut{Segments: qnxdbg.ParseMapInfo(raw), Hex: hex.EncodeToString(raw)}, nil
		})

	addTool(s, "qconn_debug_handle_signal",
		"Choose whether the debugger intercepts (stops on) a signal or passes it through to the debugged process (QNX DSMSG Handlesig). signal is the QNX signal number (1-72); stop=true makes the debugger stop on it. Sessions default to intercepting all signals.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in handleSigIn) (*mcp.CallToolResult, okOut, error) {
			if in.Signal < 1 || in.Signal > qnxdbg.NumSignals {
				return fail2[okOut]("signal %d out of range (1-%d)", in.Signal, qnxdbg.NumSignals)
			}
			sess, err := s.getSession(in.SessionID)
			if err != nil {
				return nil, okOut{}, err
			}
			sess.mu.Lock()
			defer sess.mu.Unlock()
			tbl := sess.signalTable()
			if in.Stop {
				tbl[in.Signal-1] = 1
			} else {
				tbl[in.Signal-1] = 0
			}
			if err := sess.client.HandleSig(ctx, tbl); err != nil {
				return nil, okOut{}, err
			}
			return nil, okOut{OK: true}, nil
		})

	addTool(s, "qconn_debug_target_info",
		"Read the target CPU/info block for the debug session (QNX DSMSG CPUInfo): raw hex plus any printable strings (e.g. the boot/executable path).",
		func(ctx context.Context, _ *mcp.CallToolRequest, in sessionIn) (*mcp.CallToolResult, rawInfoOut, error) {
			sess, err := s.getSession(in.SessionID)
			if err != nil {
				return nil, rawInfoOut{}, err
			}
			sess.mu.Lock()
			defer sess.mu.Unlock()
			b, err := sess.client.CPUInfo(ctx)
			if err != nil {
				return nil, rawInfoOut{}, err
			}
			return nil, rawInfoOut{Hex: hex.EncodeToString(b), Strings: printableStrings(b)}, nil
		})

	addTool(s, "qconn_debug_threads",
		"List the debugged process's threads (QNX DSMSG Pidlist/TIDNames): process name, thread names, and the raw info blocks.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in sessionIn) (*mcp.CallToolResult, threadsOut, error) {
			sess, err := s.getSession(in.SessionID)
			if err != nil {
				return nil, threadsOut{}, err
			}
			sess.mu.Lock()
			defer sess.mu.Unlock()
			out := threadsOut{PID: sess.pid}
			if pi, err := sess.client.ProcInfo(ctx, sess.pid, 1); err == nil {
				out.ProcInfoHex = hex.EncodeToString(pi)
				out.Name = longestString(printableStrings(pi)) // the process path
			}
			if tn, err := sess.client.ThreadNames(ctx); err == nil {
				out.ThreadNamesHex = hex.EncodeToString(tn)
				out.ThreadNames = printableStrings(tn)
			}
			return nil, out, nil
		})

	addTool(s, "qconn_debug_detach",
		"Detach the debugger (leaving the process running) and close the session.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in sessionIn) (*mcp.CallToolResult, okOut, error) {
			sess, err := s.getSession(in.SessionID)
			if err != nil {
				return nil, okOut{}, err
			}
			sess.mu.Lock()
			_ = sess.client.Detach(ctx, sess.pid)
			_ = sess.client.Close()
			sess.mu.Unlock()
			s.dbgMu.Lock()
			delete(s.sessions, in.SessionID)
			s.dbgMu.Unlock()
			return nil, okOut{OK: true}, nil
		})
}

type dbgReadMemOut struct {
	Hex string `json:"hex"`
}

// printableStrings extracts NUL-separated runs of printable ASCII (length >= 2)
// from a raw DSMSG reply block — used to surface names/paths embedded in the
// cpuinfo / process-info / thread-name structs without parsing every field.
func printableStrings(b []byte) []string {
	var out []string
	cur := make([]byte, 0, 32)
	flush := func() {
		if len(cur) >= 2 {
			out = append(out, string(cur))
		}
		cur = cur[:0]
	}
	for _, c := range b {
		if c >= 0x20 && c < 0x7f {
			cur = append(cur, c)
		} else {
			flush()
		}
	}
	flush()
	return out
}

// longestString returns the longest entry (the most name/path-like one).
func longestString(ss []string) string {
	best := ""
	for _, s := range ss {
		if len(s) > len(best) {
			best = s
		}
	}
	return best
}

// decodeAArch64 decodes the QNX aarch64 register area: gpr[0..30]=x0-x30,
// gpr[31]=sp, then elr(pc) at 0x100 and pstate at 0x108.
func decodeAArch64(b []byte) map[string]string {
	out := map[string]string{}
	rd := func(off int) (uint64, bool) {
		if off+8 > len(b) {
			return 0, false
		}
		return binary.LittleEndian.Uint64(b[off : off+8]), true
	}
	for i := 0; i <= 30; i++ {
		if v, ok := rd(i * 8); ok {
			out[fmt.Sprintf("x%d", i)] = fmt.Sprintf("%#x", v)
		}
	}
	if v, ok := rd(31 * 8); ok {
		out["sp"] = fmt.Sprintf("%#x", v)
	}
	if v, ok := rd(0x100); ok {
		out["pc"] = fmt.Sprintf("%#x", v)
	}
	if v, ok := rd(0x108); ok {
		out["pstate"] = fmt.Sprintf("%#x", v)
	}
	return out
}
