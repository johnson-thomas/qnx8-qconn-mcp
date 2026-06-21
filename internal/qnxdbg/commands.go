package qnxdbg

import (
	"context"
	"encoding/binary"
	"fmt"
)

// Attach attaches the debug agent to an existing process.
func (c *Client) Attach(ctx context.Context, pid int) error {
	p, err := c.transact(cmdAttach, le32(uint32(pid)))
	if err != nil {
		return err
	}
	if respCode(p) == respErr {
		return fmt.Errorf("attach pid %d: target returned error", pid)
	}
	return nil
}

// Detach detaches from a process, leaving it running.
func (c *Client) Detach(ctx context.Context, pid int) error {
	p, err := c.transact(cmdDetach, le32(uint32(pid)))
	if err != nil {
		return err
	}
	if respCode(p) == respErr {
		return fmt.Errorf("detach pid %d: target returned error", pid)
	}
	return nil
}

// Kill terminates the process pid on the target (DSMSG Kill).
func (c *Client) Kill(ctx context.Context, pid int) error {
	p, err := c.transact(cmdKill, le32(uint32(pid)))
	if err != nil {
		return err
	}
	if respCode(p) == respErr {
		return fmt.Errorf("kill pid %d: target error", pid)
	}
	return nil
}

// Select sets the current process/thread context for subsequent operations.
func (c *Client) Select(ctx context.Context, pid, tid int) error {
	body := append(le32(uint32(pid)), le32(uint32(tid))...)
	p, err := c.transact(cmdSelect, body)
	if err != nil {
		return err
	}
	if respCode(p) == respErr {
		return fmt.Errorf("select pid %d tid %d: target error", pid, tid)
	}
	return nil
}

// maxChunk is pdebug's per-read memory limit (it returns at most ~1 KiB).
const maxChunk = 0x400

// ReadMemory reads n bytes from the attached process at virtual address addr.
// Body layout (from rpdbg.py): spare0[4] addr[8] size[2] spare1[6].
func (c *Client) ReadMemory(ctx context.Context, addr uint64, n int) ([]byte, error) {
	var out []byte
	for len(out) < n {
		want := n - len(out)
		if want > maxChunk {
			want = maxChunk
		}
		body := make([]byte, 0, 20)
		body = append(body, make([]byte, 4)...)             // spare0
		body = append(body, le64(addr+uint64(len(out)))...) // addr
		body = append(body, le16(uint16(want))...)          // size
		body = append(body, make([]byte, 6)...)             // spare1
		p, err := c.transact(cmdMemrd, body)
		if err != nil {
			return out, err
		}
		if respCode(p) == respErr {
			if len(out) > 0 {
				break // partial read up to an unmapped page
			}
			return nil, fmt.Errorf("read memory @%#x: target error", addr)
		}
		data := respBody(p)
		if len(data) == 0 {
			break
		}
		if len(data) > want {
			data = data[:want]
		}
		out = append(out, data...)
		if len(data) < want {
			break // short read
		}
	}
	return out, nil
}

// WriteMemory writes data to the attached process at addr.
// Body layout (DStMsg_memwr): spare0[4] addr[8] data[].
func (c *Client) WriteMemory(ctx context.Context, addr uint64, data []byte) (int, error) {
	total := 0
	for total < len(data) {
		end := total + maxChunk
		if end > len(data) {
			end = len(data)
		}
		body := make([]byte, 0, 12+(end-total))
		body = append(body, make([]byte, 4)...)
		body = append(body, le64(addr+uint64(total))...)
		body = append(body, data[total:end]...)
		p, err := c.transact(cmdMemwr, body)
		if err != nil {
			return total, err
		}
		if respCode(p) == respErr {
			return total, fmt.Errorf("write memory @%#x: target error", addr)
		}
		total = end
	}
	return total, nil
}

// ReadRegisters reads size bytes of the register set at the given register-area
// offset. Body layout (DStMsg_regrd): offset[2] size[2]. Pass offset 0 and a
// large size to fetch the whole general register block; interpretation is
// architecture-specific (aarch64 on the RPi400).
func (c *Client) ReadRegisters(ctx context.Context, offset, size int) ([]byte, error) {
	body := append(le16(uint16(offset)), le16(uint16(size))...)
	p, err := c.transact(cmdRegrd, body)
	if err != nil {
		return nil, err
	}
	if respCode(p) == respErr {
		return nil, fmt.Errorf("read registers: target error")
	}
	return respBody(p), nil
}

// WriteRegisters writes data into the register set at offset.
func (c *Client) WriteRegisters(ctx context.Context, offset int, data []byte) error {
	body := append(le16(uint16(offset)), data...)
	p, err := c.transact(cmdRegwr, body)
	if err != nil {
		return err
	}
	if respCode(p) == respErr {
		return fmt.Errorf("write registers: target error")
	}
	return nil
}

// Breakpoint types carried in the DSMSG Brk subcmd byte (from sys/debug.h).
const (
	brkTypeExec = 0x01 // _DEBUG_BREAK_EXEC: execution breakpoint
	brkTypeRD   = 0x02 // _DEBUG_BREAK_RD:   read watchpoint
	brkTypeWR   = 0x04 // _DEBUG_BREAK_WR:   write watchpoint
	brkTypeRW   = 0x06 // _DEBUG_BREAK_RW:   read/write watchpoint
)

// SetBreakpoint sets a breakpoint of the given type at addr. size is the
// watchpoint span (1..8) or 0 for a plain execution breakpoint; size -1 removes.
//
// Wire layout (validated against real QNX 8 pdebug):
// the Brk message's subcmd byte carries the breakpoint *type* (_DEBUG_BREAK_*),
// and the body is size:int32 (0=set, -1=remove, 1..8=watchpoint) followed by
// addr:uint64. (Earlier code sent addr-then-size with subcmd 0, which pdebug
// rejected.)
func (c *Client) SetBreakpoint(ctx context.Context, addr uint64, size int) error {
	return c.setBreak(ctx, brkTypeExec, addr, size)
}

func (c *Client) setBreak(ctx context.Context, typ byte, addr uint64, size int) error {
	body := append(le32(uint32(int32(size))), le64(addr)...)
	p, err := c.transactSub(cmdBrk, typ, body)
	if err != nil {
		return err
	}
	if respCode(p) == respErr {
		return fmt.Errorf("set breakpoint @%#x: target error", addr)
	}
	return nil
}

// ClearBreakpoint removes a breakpoint at addr.
func (c *Client) ClearBreakpoint(ctx context.Context, addr uint64) error {
	return c.setBreak(ctx, brkTypeExec, addr, -1)
}

// SetWatchpoint sets a hardware data watchpoint of span size bytes at addr.
// typ is brkTypeRD/brkTypeWR/brkTypeRW.
func (c *Client) SetWatchpoint(ctx context.Context, typ byte, addr uint64, size int) error {
	if size <= 0 {
		size = 1
	}
	return c.setBreak(ctx, typ, addr, size)
}

// DSMSG Run subcmd values (validated against real QNX 8 pdebug):
// subcmd 0 = continue (free run), subcmd 1 = single-step. The single-step is
// selected by the subcmd, NOT by a debug_run flag (flags=0 for both).
const (
	runContinue = 0
	runStep     = 1
)

// runMsg builds the DSMSG Run body as a debug_run: flags[4] tid[4] followed by
// 12 reserved bytes (the truncated debug_run form used on QNX 8: flags=0,
// tid=1). The thread context is otherwise set via Select.
func (c *Client) runMsg(flags, tid uint32) []byte {
	body := make([]byte, 0, 20)
	body = append(body, le32(flags)...)
	body = append(body, le32(tid)...)
	body = append(body, make([]byte, 12)...)
	return body
}

// Continue resumes the process and waits for the next stop notification.
func (c *Client) Continue(ctx context.Context) (*Stop, error) {
	if _, err := c.transactSub(cmdRun, runContinue, c.runMsg(0, 1)); err != nil {
		return nil, err
	}
	return c.WaitStop(ctx)
}

// Step single-steps one instruction in the selected thread (tid) and waits.
func (c *Client) Step(ctx context.Context, tid int) (*Stop, error) {
	if tid <= 0 {
		tid = 1
	}
	if _, err := c.transactSub(cmdRun, runStep, c.runMsg(0, uint32(tid))); err != nil {
		return nil, err
	}
	return c.WaitStop(ctx)
}

// Stop interrupts a running process.
func (c *Client) Stop(ctx context.Context) error {
	_, err := c.transact(cmdStop, nil)
	return err
}

// Stop describes a stop notification (process halted: breakpoint, step, signal).
type Stop struct {
	Code byte   `json:"code"`
	PID  int    `json:"pid,omitempty"`
	TID  int    `json:"tid,omitempty"`
	Raw  string `json:"raw"`
}

// WaitStop reads the next asynchronous notification frame from the target.
func (c *Client) WaitStop(ctx context.Context) (*Stop, error) {
	p, err := c.readResponse()
	if err != nil {
		return nil, err
	}
	st := &Stop{Code: respCode(p), Raw: fmt.Sprintf("%x", p)}
	body := respBody(p)
	if len(body) >= 8 {
		st.PID = int(uint32(body[0]) | uint32(body[1])<<8 | uint32(body[2])<<16 | uint32(body[3])<<24)
		st.TID = int(uint32(body[4]) | uint32(body[5])<<8 | uint32(body[6])<<16 | uint32(body[7])<<24)
	}
	return st, nil
}

// ProcessList holds one entry from the pidlist enumeration.
type ProcessList struct {
	PID  int    `json:"pid"`
	TID  int    `json:"tid"`
	Raw  string `json:"raw"`
	Name string `json:"name,omitempty"`
}

// DSMSG Env (cmd 21) subcmds — used to stage the argv and environment tables on
// the target before a Load (verified against the SDK gdb on real pdebug):
const (
	envSetArgv   = 0x01 // append one argv string
	envResetEnv  = 0x02 // clear the environment table (1 KiB zero buffer)
	envSetEnv    = 0x03 // append one "NAME=VALUE" environment string
	envResetArgv = 0x00 // clear the argv table (1 KiB zero buffer)
)

// envCmd sends one Env (cmd 21) message and checks for an error reply.
func (c *Client) envCmd(sub byte, body []byte) error {
	p, err := c.transactSub(cmdEnv, sub, body)
	if err != nil {
		return err
	}
	if respCode(p) == respErr {
		return fmt.Errorf("env sub %d: %s", sub, errnoString(respBody(p)))
	}
	return nil
}

// LaunchResult is the outcome of a Load: the new process is stopped before its
// first instruction.
type LaunchResult struct {
	PID int `json:"pid"`
	TID int `json:"tid"`
}

// defaultLaunchEnv is a minimal child environment sufficient to exec a QNX
// program (the runtime loader needs PATH; LD_BIND_NOW matches the SDK gdb).
var defaultLaunchEnv = []string{"PATH=/proc/boot:/system/bin", "HOME=/", "LD_BIND_NOW=1"}

// Launch starts program path (a path on the TARGET) under debugger control,
// stopped before its first instruction — the basis for "debug from main". args
// are argv[1:]; env is the child environment as "NAME=VALUE" strings (nil uses a
// minimal default). On success the process is loaded stopped; Select the
// returned pid/tid, set breakpoints, then Continue.
//
// The sequence (Env preamble then Load) and the Load body — argc/envc zero, the
// program path, and "@N" markers that reference the argv slots staged via Env —
// were verified against the SDK gdb talking to real QNX 8 pdebug.
func (c *Client) Launch(ctx context.Context, path string, args, env []string) (*LaunchResult, error) {
	if env == nil {
		env = defaultLaunchEnv
	}
	// Stage the environment table.
	if err := c.envCmd(envResetEnv, make([]byte, 1024)); err != nil {
		return nil, err
	}
	for _, e := range env {
		if err := c.envCmd(envSetEnv, append([]byte(e), 0)); err != nil {
			return nil, err
		}
	}
	// Stage the argv table. The SDK gdb sends the program path first as the
	// exec-file, then again as argv[0], then any further args.
	if err := c.envCmd(envResetArgv, make([]byte, 1024)); err != nil {
		return nil, err
	}
	staged := append([]string{path, path}, args...) // exec-file, argv[0], argv[1:]
	for _, a := range staged {
		if err := c.envCmd(envSetArgv, append([]byte(a), 0)); err != nil {
			return nil, err
		}
	}
	// Load: argc=0, envc=0, the program path, then "@i" markers that reference
	// the staged argv slots. gdb always sends three (@0 @1 @2); send at least
	// that many so the exec-file/argv[0] slots resolve.
	n := len(staged)
	if n < 3 {
		n = 3
	}
	body := make([]byte, 8)
	body = append(body, []byte(path)...)
	body = append(body, 0)
	for i := 0; i < n; i++ {
		body = append(body, []byte(fmt.Sprintf("@%d", i))...)
		body = append(body, 0)
	}
	body = append(body, 0) // terminate the cmdline list
	p, err := c.transact(cmdLoad, body)
	if err != nil {
		return nil, err
	}
	if respCode(p) == respErr {
		return nil, fmt.Errorf("load %q: %s", path, errnoString(respBody(p)))
	}
	res := &LaunchResult{}
	if b := respBody(p); len(b) >= 8 {
		res.PID = int(binary.LittleEndian.Uint32(b[0:4]))
		res.TID = int(binary.LittleEndian.Uint32(b[4:8]))
	}
	return res, nil
}

// errnoString renders a DSMSG error-reply body (errno as little-endian int32).
func errnoString(b []byte) string {
	if len(b) < 4 {
		return "target error"
	}
	switch binary.LittleEndian.Uint32(b) {
	case 2:
		return "ENOENT (no such file or directory on the target)"
	case 8:
		return "ENOEXEC (not an executable)"
	case 13:
		return "EACCES (permission denied)"
	case 22:
		return "EINVAL (invalid argument)"
	}
	return fmt.Sprintf("target errno %d", binary.LittleEndian.Uint32(b))
}

// CPUInfo returns the target's CPU/info block (DSMSG CPUInfo). The reply is a
// cpuinfo struct followed by the boot/executable path string; callers get the
// raw bytes plus any printable trailing string.
func (c *Client) CPUInfo(ctx context.Context) ([]byte, error) {
	p, err := c.transact(cmdCPUInfo, le32(0))
	if err != nil {
		return nil, err
	}
	if respCode(p) == respErr {
		return nil, fmt.Errorf("cpuinfo: target error")
	}
	return respBody(p), nil
}

// ThreadNames returns the DSMSG TIDNames block: a packed list of thread-name
// strings for the selected process. Returned raw; the names are NUL-separated.
func (c *Client) ThreadNames(ctx context.Context) ([]byte, error) {
	p, err := c.transact(cmdTIDNames, le32(0))
	if err != nil {
		return nil, err
	}
	if respCode(p) == respErr {
		return nil, fmt.Errorf("tidnames: target error")
	}
	return respBody(p), nil
}

// ProcInfo fetches the debug_process_info block for pid/tid (DSMSG Pidlist
// subcmd 2 — the documented process-info form), which carries the process name and
// thread count. Returned raw for best-effort parsing by the caller.
func (c *Client) ProcInfo(ctx context.Context, pid, tid int) ([]byte, error) {
	body := append(le32(uint32(pid)), le32(uint32(tid))...)
	p, err := c.transactSub(cmdPidlist, 2, body)
	if err != nil {
		return nil, err
	}
	if respCode(p) == respErr {
		return nil, fmt.Errorf("procinfo pid %d: target error", pid)
	}
	return respBody(p), nil
}

// Pidlist enumerates processes/threads. subcmd 0 = first match, 1 = next.
func (c *Client) Pidlist(ctx context.Context, pid, tid int, next bool) (*ProcessList, error) {
	sub := byte(0)
	if next {
		sub = 1
	}
	body := append(le32(uint32(pid)), le32(uint32(tid))...)
	p, err := c.transactSub(cmdPidlist, sub, body)
	if err != nil {
		return nil, err
	}
	if respCode(p) == respErr {
		return nil, fmt.Errorf("pidlist: target error")
	}
	b := respBody(p)
	pl := &ProcessList{Raw: fmt.Sprintf("%x", b)}
	if len(b) >= 8 {
		pl.PID = int(uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24)
		pl.TID = int(uint32(b[4]) | uint32(b[5])<<8 | uint32(b[6])<<16 | uint32(b[7])<<24)
	}
	return pl, nil
}
