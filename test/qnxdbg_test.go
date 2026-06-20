package integration

import (
	"context"
	"encoding/binary"
	"net"
	"testing"
	"time"

	"github.com/johnson-thomas/qnx8-qconn-mcp/internal/mockqnx"
	"github.com/johnson-thomas/qnx8-qconn-mcp/internal/qnxdbg"
)

// TestQNXDebugProtocol exercises the QNX DSMSG client against the mock pdebug:
// handshake, attach, select, register read (decoded pc), memory read, detach.
func TestQNXDebugProtocol(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go mockqnx.ListenDSMSG(ln)
	t.Cleanup(func() { ln.Close() })

	ctx := context.Background()
	c, err := qnxdbg.Connect(ctx, ln.Addr().String(), nil, 5*time.Second)
	if err != nil {
		t.Fatalf("connect/handshake: %v", err)
	}
	t.Cleanup(func() { c.Close() })

	if err := c.Attach(ctx, 4242); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if err := c.Select(ctx, 4242, 1); err != nil {
		t.Fatalf("select: %v", err)
	}

	regs, err := c.ReadRegisters(ctx, 0, 0x110)
	if err != nil {
		t.Fatalf("regrd: %v", err)
	}
	if len(regs) != 0x110 {
		t.Fatalf("regs len=%d want 0x110", len(regs))
	}
	if pc := binary.LittleEndian.Uint64(regs[0x100:]); pc != 0x4000 {
		t.Fatalf("pc=%#x want 0x4000", pc)
	}

	const base = 0x10000
	mem, err := c.ReadMemory(ctx, base, 64)
	if err != nil {
		t.Fatalf("memrd: %v", err)
	}
	if len(mem) != 64 {
		t.Fatalf("mem len=%d want 64", len(mem))
	}
	for i, b := range mem {
		if want := byte((base + i) & 0xff); b != want {
			t.Fatalf("mem[%d]=%#x want %#x", i, b, want)
		}
	}

	// Breakpoint set + continue should produce a stop notification at the
	// breakpoint address (the mock reports the last set brk as the stop IP).
	const bp = 0x4012ab
	if err := c.SetBreakpoint(ctx, bp, 0); err != nil {
		t.Fatalf("set breakpoint: %v", err)
	}
	st, err := c.Continue(ctx)
	if err != nil {
		t.Fatalf("continue: %v", err)
	}
	if st.Code != 0x40 {
		t.Fatalf("stop code=%#x want 0x40 (notify)", st.Code)
	}
	if st.PID != 4242 {
		t.Fatalf("stop pid=%d want 4242", st.PID)
	}
	if sst, err := c.Step(ctx, 1); err != nil {
		t.Fatalf("step: %v", err)
	} else if sst.Code != 0x40 {
		t.Fatalf("step stop code=%#x want 0x40", sst.Code)
	}
	if err := c.ClearBreakpoint(ctx, bp); err != nil {
		t.Fatalf("clear breakpoint: %v", err)
	}

	if err := c.Detach(ctx, 4242); err != nil {
		t.Fatalf("detach: %v", err)
	}
}
