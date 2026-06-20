// Package qconn implements a client for the QNX qconn target-agent protocol.
//
// qconn is a line-oriented, telnet-ish TCP protocol (default port 8000) used by
// the QNX Momentics IDE to talk to a target. A "broker" greets the connection
// and multiplexes named sub-services that are entered with "service <name>":
//
//	broker    - versions/handshake; entry point
//	launcher  - spawn processes, capture stdout/stderr, run pdebug for debugging
//	file      - open/read/write/close/delete/chmod files (incl. /proc nodes)
//	cntl      - send signals to processes
//	sinfo     - system information (where available)
//
// The protocol and framing were derived from the public qcl.pl client
// (github.com/zayfod/qcl), the nmap qconn-exec script, and the QNX IDE docs, and
// cross-checked against the openqnx (~6.4) primitives qconn surfaces (/proc,
// pidin, pdebug). See README.md for the full protocol reference.
package qconn

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Service identifies a qconn sub-service.
type Service string

const (
	ServiceBroker   Service = "broker"
	ServiceLauncher Service = "launcher"
	ServiceFile     Service = "file"
	ServiceCntl     Service = "cntl"
	ServiceSinfo    Service = "sinfo"
)

// DefaultPort is the well-known qconn TCP port.
const DefaultPort = 8000

// Config configures a Client.
type Config struct {
	Host    string
	Port    int
	Timeout time.Duration // per-operation I/O deadline; 0 disables
	Logger  *slog.Logger
}

// Client is a synchronized connection to a qconn daemon. It is safe for
// concurrent use: a mutex serializes the inherently stateful, prompt-based
// request/response exchanges.
type Client struct {
	cfg     Config
	log     *slog.Logger
	mu      sync.Mutex
	fc      *frameConn
	current Service // service the connection is currently in
	info    map[string]string
}

// Dial connects to a qconn daemon and completes the broker handshake.
func Dial(ctx context.Context, cfg Config) (*Client, error) {
	if cfg.Port == 0 {
		cfg.Port = DefaultPort
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 15 * time.Second
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))

	d := net.Dialer{Timeout: cfg.Timeout}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("qconn dial %s: %w", addr, err)
	}
	c := &Client{
		cfg:     cfg,
		log:     cfg.Logger.With("component", "qconn", "target", addr),
		fc:      newFrameConn(conn, cfg.Logger, cfg.Timeout),
		current: ServiceBroker,
	}
	if err := c.handshake(); err != nil {
		conn.Close()
		return nil, err
	}
	c.log.Info("qconn connected")
	return c, nil
}

// Close terminates the connection, attempting a polite broker quit first.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.fc.writeLine("quit")
	return c.fc.close()
}

// handshake consumes the greeting: telnet negotiation, the "QCONN" banner, the
// broker prompt, and the (tolerated) linemode/echo error line.
func (c *Client) handshake() error {
	// Read until the first broker prompt. The banner ("QCONN") and the
	// "error linemode-or-echo-not-supported" line arrive within this window and
	// are simply part of the consumed text.
	banner, err := c.fc.readUntilPrompt()
	if err != nil {
		return fmt.Errorf("qconn handshake: %w", err)
	}
	if !strings.Contains(banner, "QCONN") {
		// Some builds may not include the literal banner before the prompt; the
		// prompt match above is the real success signal, so only warn.
		c.log.Warn("qconn handshake missing QCONN banner", "banner", strings.TrimSpace(banner))
	}
	// Keep telnet/IAC handling active for the life of the connection: real qconn
	// emits IAC option negotiation mid-stream (e.g. DO SGA before a response).
	// Binary file payloads are read via readN (which bypasses IAC), so leaving
	// telnet processing on does not corrupt them.
	return nil
}

// raw runs a single command in the current service and returns the response
// block (text up to, but excluding, the next prompt).
func (c *Client) raw(cmd string) (string, error) {
	if err := c.fc.writeLine(cmd); err != nil {
		return "", err
	}
	return c.fc.readUntilPrompt()
}

// Versions returns the service->version map reported by "versions ?".
func (c *Client) Versions(ctx context.Context) (map[string]int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ensure(ServiceBroker); err != nil {
		return nil, err
	}
	resp, err := c.raw("versions ?")
	if err != nil {
		return nil, err
	}
	line := firstLine(resp)
	// Real format (QNX 8): "versions: broker=257 launcher=258 file=258 ...".
	// Older docs show "name/version"; accept '=', '/', or ':' as the separator.
	out := map[string]int{}
	for _, tok := range strings.Fields(line) {
		if tok == "versions:" {
			continue
		}
		name, ver := tok, 0
		if i := strings.IndexAny(tok, "=/:"); i >= 0 {
			name = tok[:i]
			ver, _ = strconv.Atoi(strings.Trim(tok[i+1:], "=/: "))
		}
		if name != "" {
			out[name] = ver
		}
	}
	return out, nil
}

// Info returns the target system attributes from the "info" command, parsed
// into a KEY=VALUE map (ARCHITECTURE, OS, RELEASE, HOSTNAME, CPU, ENDIAN, ...).
func (c *Client) Info(ctx context.Context) (map[string]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.info != nil {
		return c.info, nil
	}
	if err := c.ensure(ServiceBroker); err != nil {
		return nil, err
	}
	resp, err := c.raw("info")
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	for _, field := range strings.Fields(resp) {
		if k, v, ok := strings.Cut(field, "="); ok {
			m[k] = v
		}
	}
	c.info = m
	return m, nil
}

// ensure switches the connection to the requested service if not already there.
// Switching is achieved with "service <name>" which the daemon answers "OK"
// before emitting the new service prompt. The broker is re-entered with "done".
func (c *Client) ensure(s Service) error {
	if c.current == s {
		return nil
	}
	// Return to the broker first if we are in a leaf service.
	if c.current != ServiceBroker {
		if err := c.leaveService(); err != nil {
			return err
		}
	}
	if s == ServiceBroker {
		return nil
	}
	resp, err := c.raw("service " + string(s))
	if err != nil {
		return err
	}
	if !strings.HasPrefix(firstLine(resp), "OK") {
		return protoErr("service "+string(s), resp)
	}
	c.current = s
	return nil
}

// leaveService returns from a leaf service back to the broker. The file service
// uses "q"; launcher/cntl use "done"/"quit" depending on build, so we try the
// generic "done" and resync on the broker prompt.
func (c *Client) leaveService() error {
	var leave string
	switch c.current {
	case ServiceFile:
		leave = "q"
	default:
		leave = "done"
	}
	if err := c.fc.writeLine(leave); err != nil {
		return err
	}
	if _, err := c.fc.readUntilPrompt(); err != nil {
		return err
	}
	c.current = ServiceBroker
	return nil
}

// Raw exposes a single command exchange in the given service for advanced or
// experimental use. It is primarily intended for protocol exploration against a
// real target via the proxy.
func (c *Client) Raw(ctx context.Context, svc Service, cmd string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ensure(svc); err != nil {
		return "", err
	}
	return c.raw(cmd)
}

// Target returns the host:port string for logging/display.
func (c *Client) Target() string {
	return net.JoinHostPort(c.cfg.Host, strconv.Itoa(c.cfg.Port))
}
