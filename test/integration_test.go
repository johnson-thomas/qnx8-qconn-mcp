// Package integration exercises the qconn client and debug bridge end to end
// against the in-process mock qconn (internal/mockqnx). Run with: go test ./test/...
package integration

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/johnson-thomas/qnx8-qconn-mcp/internal/debug"
	"github.com/johnson-thomas/qnx8-qconn-mcp/internal/mockqnx"
	"github.com/johnson-thomas/qnx8-qconn-mcp/internal/qconn"
)

// startMock launches the mock qconn on an ephemeral port and returns host, port.
func startMock(t *testing.T) (string, int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go mockqnx.Accept(ln)
	t.Cleanup(func() { ln.Close() })
	host, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)
	return host, port
}

func dial(t *testing.T, host string, port int) *qconn.Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := qconn.Dial(ctx, qconn.Config{Host: host, Port: port, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestInfoAndVersions(t *testing.T) {
	host, port := startMock(t)
	c := dial(t, host, port)
	ctx := context.Background()

	info, err := c.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info["OS"] != "nto" || info["RELEASE"] != "8.0" {
		t.Fatalf("unexpected info: %+v", info)
	}

	vers, err := c.Versions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if vers["launcher"] != 256 || vers["file"] != 256 {
		t.Fatalf("unexpected versions: %+v", vers)
	}
}

func TestExecAndProcesses(t *testing.T) {
	host, port := startMock(t)
	c := dial(t, host, port)
	ctx := context.Background()

	r, err := c.Exec(ctx, "uname -a")
	if err != nil {
		t.Fatal(err)
	}
	if r.PID == 0 || r.Output == "" {
		t.Fatalf("exec result empty: %+v", r)
	}

	procs, raw, err := c.ListProcesses(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(procs) == 0 || raw == "" {
		t.Fatalf("no processes parsed; raw=%q", raw)
	}
	// procnto (pid 1) should have multiple threads.
	var found bool
	for _, p := range procs {
		if p.PID == 1 && p.NumThreads >= 2 {
			found = true
		}
	}
	if !found {
		t.Fatalf("did not find pid 1 with >=2 threads: %+v", procs)
	}
}

func TestProcessMemoryAndSystemMemory(t *testing.T) {
	host, port := startMock(t)
	c := dial(t, host, port)
	ctx := context.Background()

	usage, _, err := c.ProcessMemory(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(usage) == 0 {
		t.Fatal("no memory usage parsed")
	}

	sys, _, err := c.SystemMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if sys["FreeMemory"] == "" {
		t.Fatalf("missing FreeMemory: %+v", sys)
	}
}

func TestMemoryReadViaProc(t *testing.T) {
	host, port := startMock(t)
	c := dial(t, host, port)
	ctx := context.Background()

	const base = 0x1000
	const n = 300 // spans more than one 2KB read chunk boundary logic
	b, err := c.ReadMemory(ctx, 8200, base, n)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != n {
		t.Fatalf("read %d bytes want %d", len(b), n)
	}
	// Mock memory device: byte at addr A == A & 0xff.
	for i := 0; i < n; i++ {
		if want := byte((base + i) & 0xff); b[i] != want {
			t.Fatalf("byte %d = %#x want %#x", i, b[i], want)
		}
	}
}

func TestMemoryMap(t *testing.T) {
	host, port := startMock(t)
	c := dial(t, host, port)
	segs, raw, err := c.MemoryMap(context.Background(), 8200)
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) == 0 {
		t.Fatalf("no segments parsed; raw=%q", raw)
	}
	if segs[0].Start != 0x08048000 || segs[0].End != 0x08050000 {
		t.Fatalf("unexpected first segment: %+v", segs[0])
	}
}

func TestFileRoundTrip(t *testing.T) {
	host, port := startMock(t)
	c := dial(t, host, port)
	ctx := context.Background()

	content := []byte("hello qnx\nsecond line\n")
	n, err := c.WriteFile(ctx, "/tmp/test.txt", content, 0644)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(content) {
		t.Fatalf("wrote %d want %d", n, len(content))
	}
	got, err := c.ReadFile(ctx, "/tmp/test.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Fatalf("read back %q want %q", got, content)
	}
}

func TestSignal(t *testing.T) {
	host, port := startMock(t)
	c := dial(t, host, port)
	if err := c.Signal(context.Background(), 8300, qconn.SIGTERM); err != nil {
		t.Fatal(err)
	}
}

func TestDebugSession(t *testing.T) {
	host, port := startMock(t)
	c := dial(t, host, port)
	ctx := context.Background()

	mgr := debug.NewManager(c, host, nil)
	// Manager spawns "pdebug <port>" via the mock launcher (which starts a mock
	// RSP responder on that port), then dials it and attaches.
	sess, err := mgr.Attach(ctx, 8200, 18001)
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	t.Cleanup(func() { sess.Close() })

	st, err := sess.Continue()
	if err != nil {
		t.Fatal(err)
	}
	if st.Signal != 5 {
		t.Fatalf("continue stop signal=%d want 5", st.Signal)
	}

	regs, err := sess.ReadRegisters()
	if err != nil {
		t.Fatal(err)
	}
	if len(regs) == 0 {
		t.Fatal("empty registers")
	}

	mem, err := sess.ReadMemory(0x2000, 16)
	if err != nil {
		t.Fatal(err)
	}
	for i, b := range mem {
		if want := byte((0x2000 + i) & 0xff); b != want {
			t.Fatalf("dbg mem byte %d=%#x want %#x", i, b, want)
		}
	}

	if err := sess.SetBreakpoint(0x4012ab, debug.SWBreak, 1); err != nil {
		t.Fatal(err)
	}
	threads, err := sess.ListThreads()
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) == 0 {
		t.Fatal("no threads listed")
	}
	if err := sess.Detach(); err != nil {
		t.Fatal(err)
	}
}
