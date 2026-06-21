package qnxdbg

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"net"
	"time"

	"github.com/johnson-thomas/qnx8-qconn-mcp/internal/obs"
)

// transport is the byte stream pdebug is reached over. *net.Conn (TCP) and
// *os.File (a raw serial port) both satisfy it, so the DSMSG client works
// unchanged over either link.
type transport interface {
	io.Reader
	io.Writer
	io.Closer
	SetDeadline(t time.Time) error
}

// Client is a connection to a pdebug agent speaking the QNX DSMSG protocol.
type Client struct {
	conn    transport
	br      *bufio.Reader
	log     *slog.Logger
	timeout time.Duration
	remote  string // human label for logs (host:port or serial device)
	mid     byte   // message id (low byte of the counter; channel high byte = DEBUG)
	output  []byte // accumulated program stdout/stderr from the text channel
}

// Connect dials pdebug at addr (host:port) and performs the connect handshake.
func Connect(ctx context.Context, addr string, log *slog.Logger, timeout time.Duration) (*Client, error) {
	if log == nil {
		log = slog.Default()
	}
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("qnxdbg dial %s: %w", addr, err)
	}
	return newClient(conn, addr, log, timeout)
}

// newClient wraps an established transport and runs the connect handshake.
func newClient(conn transport, remote string, log *slog.Logger, timeout time.Duration) (*Client, error) {
	if log == nil {
		log = slog.Default()
	}
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	c := &Client{conn: conn, br: bufio.NewReaderSize(conn, 16*1024), log: log, timeout: timeout, remote: remote}
	if err := c.handshake(); err != nil {
		conn.Close()
		return nil, err
	}
	return c, nil
}

// Close terminates the connection.
func (c *Client) Close() error { return c.conn.Close() }

func (c *Client) deadline() {
	if c.timeout > 0 {
		_ = c.conn.SetDeadline(time.Now().Add(c.timeout))
	}
}

// writeFrame sends a complete frame for payload.
func (c *Client) writeFrame(payload []byte) error {
	c.deadline()
	b := frame(payload)
	obs.Trace(c.log, "qnxdbg", ">>", b)
	_, err := c.conn.Write(b)
	return err
}

// readFrame reads one frame and returns its verified payload (header+body, no
// checksum). Runs of 0x7e delimiters are skipped, so adjacent frames parse.
func (c *Client) readFrame() ([]byte, error) {
	// Skip leading delimiters to the first content byte.
	var first byte
	for {
		c.deadline()
		b, err := c.br.ReadByte()
		if err != nil {
			return nil, err
		}
		if b != frameChar {
			first = b
			break
		}
	}
	content := []byte{first}
	for {
		c.deadline()
		b, err := c.br.ReadByte()
		if err != nil {
			return nil, err
		}
		if b == frameChar {
			break
		}
		content = append(content, b)
	}
	dec := unescape(content)
	if len(dec) < 1 {
		return nil, fmt.Errorf("qnxdbg: empty frame")
	}
	payload, cs := dec[:len(dec)-1], dec[len(dec)-1]
	if checksum(payload) != cs {
		return nil, fmt.Errorf("qnxdbg: bad checksum (got %#x want %#x)", cs, checksum(payload))
	}
	obs.Trace(c.log, "qnxdbg", "<<", append(append([]byte{frameChar}, content...), frameChar))
	return payload, nil
}

// readResponse reads frames and returns the first real DSMSG reply or
// notification on the DEBUG channel. It skips channel-control frames (payload <
// 4 bytes) and TEXT-channel frames (channel byte == chanText) — the latter carry
// the debugged process's stdout/stderr when pdebug owns the program (i.e. after
// a Load), which must not be mistaken for a debug reply. Text payloads are
// accumulated in c.output for retrieval.
func (c *Client) readResponse() ([]byte, error) {
	for {
		p, err := c.readFrame()
		if err != nil {
			return nil, err
		}
		if len(p) < 4 {
			continue // control frame (e.g. channel set)
		}
		if p[3] == chanText {
			c.output = append(c.output, p[4:]...) // program stdout/stderr
			continue
		}
		return p, nil
	}
}

// Output returns and clears any program stdout/stderr captured from the debug
// connection's text channel (relevant for launched processes).
func (c *Client) Output() []byte {
	out := c.output
	c.output = nil
	return out
}

// handshake performs the connect sequence: echo the reset frame, send Connect,
// then Protover, consuming the control/status replies.
func (c *Client) handshake() error {
	reset, err := c.readFrame() // expect channel-reset control frame
	if err != nil {
		return fmt.Errorf("qnxdbg handshake (reset): %w", err)
	}
	if err := c.writeFrame(reset); err != nil { // echo it back
		return err
	}
	// TargetConnect, mid=0, channel=DEBUG; body is the host protocol version.
	c.mid = 0
	if err := c.writeFrame(c.header(cmdConnect, 0, []byte{0x00, 0x07, 0x00, 0x00})); err != nil {
		return err
	}
	if _, err := c.readResponse(); err != nil {
		return fmt.Errorf("qnxdbg handshake (connect): %w", err)
	}
	// TargetProtover, mid=1.
	c.mid = 1
	if err := c.writeFrame(c.header(cmdProtover, 0, []byte{0x00, 0x07})); err != nil {
		return err
	}
	if _, err := c.readResponse(); err != nil {
		return fmt.Errorf("qnxdbg handshake (protover): %w", err)
	}
	c.log.Info("qnxdbg connected", "target", c.remote)
	return nil
}

// header builds a DSMSG payload: [cmd, subcmd, mid, channel=DEBUG] + body.
// (The 16-bit counter is mid | (DEBUG<<8); starting at 0x100 keeps channel=1.)
func (c *Client) header(cmd, subcmd byte, body []byte) []byte {
	p := []byte{cmd, subcmd, c.mid, chanDebug}
	return append(p, body...)
}

// transact increments the message id, sends cmd+body (subcmd 0), and returns the
// reply payload. The 4-byte reply header is [code, subcmd, mid, channel].
func (c *Client) transact(cmd byte, body []byte) ([]byte, error) {
	return c.transactSub(cmd, 0, body)
}

// transactSub is transact with an explicit subcmd.
func (c *Client) transactSub(cmd, subcmd byte, body []byte) ([]byte, error) {
	c.mid++ // wraps at 256; channel stays DEBUG
	if err := c.writeFrame(c.header(cmd, subcmd, body)); err != nil {
		return nil, err
	}
	return c.readResponse()
}

// respCode returns the reply's response code (payload[0]).
func respCode(p []byte) byte {
	if len(p) == 0 {
		return 0
	}
	return p[0]
}

// respBody returns the reply body (after the 4-byte DSMSG header).
func respBody(p []byte) []byte {
	if len(p) <= 4 {
		return nil
	}
	return p[4:]
}

// le16 / le32 / le64 little-endian encoders for message bodies.
func le16(v uint16) []byte { b := make([]byte, 2); binary.LittleEndian.PutUint16(b, v); return b }
func le32(v uint32) []byte { b := make([]byte, 4); binary.LittleEndian.PutUint32(b, v); return b }
func le64(v uint64) []byte { b := make([]byte, 8); binary.LittleEndian.PutUint64(b, v); return b }
