// Package debug implements a GDB Remote Serial Protocol (RSP) client used to
// drive QNX's pdebug agent for source-/machine-level debugging.
//
// On QNX, the Momentics IDE debugs a target by having qconn launch pdebug (a
// gdbserver-equivalent stub) and then speaking GDB RSP to it. This package
// provides the RSP codec (rsp.go), the high-level debug session operations
// (session.go), and the orchestration that starts pdebug through qconn and
// connects to it (pdebug.go).
//
// References: the GDB Remote Serial Protocol specification and QNX's pdebug;
// cross-checked against openqnx primitives. See README.md.
package debug

import (
	"bufio"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"

	"github.com/johnson-thomas/qnx8-qconn-mcp/internal/obs"
)

// RSP is a framed GDB Remote Serial Protocol connection.
type RSP struct {
	conn    net.Conn
	br      *bufio.Reader
	log     *slog.Logger
	timeout time.Duration
	noAck   bool
}

// NewRSP wraps a connection as an RSP endpoint.
func NewRSP(conn net.Conn, log *slog.Logger, timeout time.Duration) *RSP {
	if log == nil {
		log = slog.Default()
	}
	return &RSP{
		conn:    conn,
		br:      bufio.NewReaderSize(conn, 32*1024),
		log:     log,
		timeout: timeout,
	}
}

func (r *RSP) deadline() {
	if r.timeout > 0 {
		_ = r.conn.SetDeadline(time.Now().Add(r.timeout))
	}
}

// Close closes the underlying connection.
func (r *RSP) Close() error { return r.conn.Close() }

// checksum is the modulo-256 sum of the payload bytes.
func checksum(p []byte) byte {
	var s byte
	for _, b := range p {
		s += b
	}
	return s
}

// escape encodes RSP-special bytes ($ # } *) using the }^0x20 escape.
func escape(p []byte) []byte {
	out := make([]byte, 0, len(p))
	for _, b := range p {
		switch b {
		case '$', '#', '}', '*':
			out = append(out, '}', b^0x20)
		default:
			out = append(out, b)
		}
	}
	return out
}

// SendPacket frames and sends a payload, then waits for the '+' ack (unless in
// no-ack mode), retransmitting on '-'.
func (r *RSP) SendPacket(payload string) error {
	body := escape([]byte(payload))
	frame := make([]byte, 0, len(body)+4)
	frame = append(frame, '$')
	frame = append(frame, body...)
	frame = append(frame, '#')
	frame = append(frame, hexByte(checksum(body))...)

	for attempt := 0; attempt < 5; attempt++ {
		r.deadline()
		obs.Trace(r.log, "rsp", ">>", frame)
		if _, err := r.conn.Write(frame); err != nil {
			return err
		}
		if r.noAck {
			return nil
		}
		ack, err := r.readAck()
		if err != nil {
			return err
		}
		if ack == '+' {
			return nil
		}
		// '-' => retransmit.
	}
	return errors.New("rsp: too many NAKs")
}

// readAck reads a single '+' or '-' acknowledgement, skipping stray bytes.
func (r *RSP) readAck() (byte, error) {
	for {
		r.deadline()
		b, err := r.br.ReadByte()
		if err != nil {
			return 0, err
		}
		if b == '+' || b == '-' {
			return b, nil
		}
		// Some stubs may interleave; ignore anything else.
	}
}

// ReadPacket reads one packet payload, validates the checksum, sends an ack
// (unless in no-ack mode), and decodes escaping + run-length encoding.
func (r *RSP) ReadPacket() (string, error) {
	// Find packet start '$', tolerating leading acks/notifications.
	for {
		r.deadline()
		b, err := r.br.ReadByte()
		if err != nil {
			return "", err
		}
		if b == '$' {
			break
		}
		if b == '+' || b == '-' {
			continue
		}
		// Ignore other noise (e.g., '%' notifications would need handling, but
		// pdebug does not use them for our command set).
	}
	var raw []byte
	for {
		b, err := r.br.ReadByte()
		if err != nil {
			return "", err
		}
		if b == '#' {
			break
		}
		raw = append(raw, b)
	}
	// Two checksum hex digits.
	hi, err := r.br.ReadByte()
	if err != nil {
		return "", err
	}
	lo, err := r.br.ReadByte()
	if err != nil {
		return "", err
	}
	obs.Trace(r.log, "rsp", "<<", append(append([]byte{'$'}, raw...), '#', hi, lo))

	want := fromHex(hi)<<4 | fromHex(lo)
	if checksum(raw) != byte(want) {
		if !r.noAck {
			_, _ = r.conn.Write([]byte{'-'})
		}
		return "", fmt.Errorf("rsp: bad checksum")
	}
	if !r.noAck {
		r.deadline()
		if _, err := r.conn.Write([]byte{'+'}); err != nil {
			return "", err
		}
	}
	return string(decode(raw)), nil
}

// Command sends a packet and returns the next response packet.
func (r *RSP) Command(payload string) (string, error) {
	if err := r.SendPacket(payload); err != nil {
		return "", err
	}
	return r.ReadPacket()
}

// EnableNoAck negotiates QStartNoAckMode to reduce round-trips (optional).
func (r *RSP) EnableNoAck() error {
	resp, err := r.Command("QStartNoAckMode")
	if err != nil {
		return err
	}
	if resp == "OK" {
		r.noAck = true
	}
	return nil
}

// decode reverses escaping and run-length encoding in a received payload.
func decode(p []byte) []byte {
	out := make([]byte, 0, len(p))
	for i := 0; i < len(p); i++ {
		switch p[i] {
		case '}': // escape: next byte XOR 0x20
			if i+1 < len(p) {
				i++
				out = append(out, p[i]^0x20)
			}
		case '*': // run-length: previous char repeated (count = next-29)
			if i+1 < len(p) && len(out) > 0 {
				count := int(p[i+1]) - 29
				last := out[len(out)-1]
				for j := 0; j < count; j++ {
					out = append(out, last)
				}
				i++
			}
		default:
			out = append(out, p[i])
		}
	}
	return out
}

func hexByte(b byte) []byte {
	const hexdigits = "0123456789abcdef"
	return []byte{hexdigits[b>>4], hexdigits[b&0xf]}
}

func fromHex(b byte) int {
	switch {
	case b >= '0' && b <= '9':
		return int(b - '0')
	case b >= 'a' && b <= 'f':
		return int(b-'a') + 10
	case b >= 'A' && b <= 'F':
		return int(b-'A') + 10
	}
	return 0
}

// hexEncode renders bytes as lowercase hex (for M/X payloads).
func hexEncode(b []byte) string {
	var sb strings.Builder
	for _, c := range b {
		sb.Write(hexByte(c))
	}
	return sb.String()
}

// hexDecode parses a lowercase/uppercase hex string into bytes.
func hexDecode(s string) ([]byte, error) {
	if len(s)%2 != 0 {
		return nil, fmt.Errorf("odd-length hex %q", s)
	}
	out := make([]byte, len(s)/2)
	for i := 0; i < len(out); i++ {
		out[i] = byte(fromHex(s[2*i])<<4 | fromHex(s[2*i+1]))
	}
	return out, nil
}
