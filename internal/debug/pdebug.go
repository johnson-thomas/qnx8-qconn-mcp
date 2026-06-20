package debug

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"time"

	"github.com/johnson-thomas/qnx8-qconn-mcp/internal/qconn"
)

// Manager orchestrates pdebug-based debugging on a QNX target. It uses a qconn
// client to launch the pdebug agent and then connects to it over a separate TCP
// connection.
//
// PROTOCOL NOTE (validated against a real QNX 8 RaspberryPi400): QNX `pdebug`
// does NOT speak the GDB Remote Serial Protocol — on connect it emits the QNX
// channel framing ("~\x00\xff~", 0x7e-delimited) and ignores "$...#cs" packets.
// The real pdebug protocol (the 0x7e-framed DSMSG protocol) is implemented in
// package internal/qnxdbg, which the MCP qconn_debug_* tools use; that path is
// validated against real pdebug (attach, register read, memory read/write,
// detach). The [Session]/[RSP] machinery in THIS package is a separate GDB-RSP
// bridge for gdbserver-style stubs (and the mock RSP responder) and is retained
// for that use.
//
// This Manager is still used to spawn pdebug on the target: the launcher form
// uses `on -d` so pdebug survives the one-shot launcher connection closing (a
// plain background job is reaped when the launcher session ends).
type Manager struct {
	Client    *qconn.Client
	Host      string
	Logger    *slog.Logger
	Timeout   time.Duration
	PdebugCmd string // printf template taking the port, e.g. "pdebug %d"
}

// NewManager builds a Manager with sensible defaults.
func NewManager(c *qconn.Client, host string, log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	return &Manager{
		Client:  c,
		Host:    host,
		Logger:  log.With("component", "pdebug"),
		Timeout: 20 * time.Second,
		// `on -d` detaches pdebug so it survives the one-shot launcher session
		// closing; "%d" is the TCP port pdebug accepts connections on.
		PdebugCmd: "on -d pdebug %d",
	}
}

// DialRSP connects directly to an already-listening pdebug/gdbserver at
// host:port and returns a debug Session. Useful for the mock and for setups
// where pdebug is started out of band.
func (m *Manager) DialRSP(ctx context.Context, host string, port int) (*Session, error) {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	d := net.Dialer{Timeout: m.Timeout}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dial pdebug %s: %w", addr, err)
	}
	rsp := NewRSP(conn, m.Logger, m.Timeout)
	return NewSession(rsp)
}

// SpawnPdebug launches the pdebug agent on the target listening on dbgPort.
func (m *Manager) SpawnPdebug(ctx context.Context, dbgPort int) error {
	if m.Client == nil {
		return fmt.Errorf("no qconn client configured")
	}
	cmd := fmt.Sprintf(m.PdebugCmd, dbgPort)
	pid, err := m.Client.Run(ctx, cmd)
	if err != nil {
		return fmt.Errorf("spawn pdebug: %w", err)
	}
	m.Logger.Info("spawned pdebug", "cmd", cmd, "pid", pid, "port", dbgPort)
	// Give pdebug a moment to bind its listening socket.
	time.Sleep(400 * time.Millisecond)
	return nil
}

// Attach launches pdebug on dbgPort (via qconn) and attaches to an existing pid.
func (m *Manager) Attach(ctx context.Context, pid, dbgPort int) (*Session, error) {
	host := m.Host
	if host == "" {
		host = "127.0.0.1"
	}
	if err := m.SpawnPdebug(ctx, dbgPort); err != nil {
		return nil, err
	}
	sess, err := m.DialRSP(ctx, host, dbgPort)
	if err != nil {
		return nil, err
	}
	if _, err := sess.Attach(pid); err != nil {
		sess.Close()
		return nil, fmt.Errorf("attach pid %d: %w", pid, err)
	}
	m.Logger.Info("attached", "pid", pid, "port", dbgPort)
	return sess, nil
}
