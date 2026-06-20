package qconn

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"net"
	"regexp"
	"strings"
	"time"

	"github.com/johnson-thomas/qnx8-qconn-mcp/internal/obs"
)

// Telnet control bytes. qconn speaks a telnet-ish protocol: on connect it
// negotiates options, and because the IDE/client refuses LINEMODE and ECHO the
// daemon emits "error linemode-or-echo-not-supported" and falls back to a plain
// line protocol. We refuse every option (reply WONT/DONT) to reach that state.
const (
	iac  = 255 // Interpret As Command
	dont = 254
	doo  = 253 // "DO"
	wont = 252
	will = 251
	sb   = 250 // subnegotiation begin
	se   = 240 // subnegotiation end
)

// promptRe matches any qconn service prompt at the end of a buffer. Prompts are
// NOT newline-terminated, so prompt detection is how we know a response is
// complete. Services: broker, launcher, file, cntl, sinfo.
var promptRe = regexp.MustCompile(`<qconn-(broker|launcher|file|cntl|sinfo)>\s*$`)

// frameConn is the low-level framed connection: it owns the socket, performs
// telnet negotiation during the handshake, and provides prompt-synchronized
// line reads plus raw binary reads/writes for the file service.
type frameConn struct {
	raw     net.Conn
	br      *bufio.Reader
	log     *slog.Logger
	timeout time.Duration

	// telnet is true only during the initial handshake; afterwards the stream
	// is a clean line protocol and 0xFF bytes are treated as data (important so
	// binary file payloads are not corrupted).
	telnet bool
}

func newFrameConn(c net.Conn, log *slog.Logger, timeout time.Duration) *frameConn {
	if log == nil {
		log = slog.Default()
	}
	return &frameConn{
		raw:     c,
		br:      bufio.NewReaderSize(c, 64*1024),
		log:     log,
		timeout: timeout,
		telnet:  true,
	}
}

func (f *frameConn) deadline() {
	if f.timeout > 0 {
		_ = f.raw.SetDeadline(time.Now().Add(f.timeout))
	}
}

// writeRaw writes bytes verbatim (no telnet escaping; qconn reads file-write
// payloads and commands as raw bytes).
func (f *frameConn) writeRaw(b []byte) error {
	f.deadline()
	obs.Trace(f.log, "qconn", ">>", b)
	_, err := f.raw.Write(b)
	return err
}

// writeLine sends a command terminated by LF. qconn's line reader is tolerant;
// we use bare LF to avoid a stray CR ending up inside command arguments.
func (f *frameConn) writeLine(s string) error {
	return f.writeRaw([]byte(s + "\n"))
}

// readByte returns the next protocol byte, transparently consuming and
// responding to telnet IAC sequences while the handshake is active.
func (f *frameConn) readByte() (byte, error) {
	for {
		f.deadline()
		b, err := f.br.ReadByte()
		if err != nil {
			return 0, err
		}
		if f.telnet && b == iac {
			if err := f.handleIAC(); err != nil {
				return 0, err
			}
			continue
		}
		return b, nil
	}
}

// handleIAC consumes one telnet command following an IAC byte and refuses any
// option negotiation (DO->WONT, WILL->DONT). Subnegotiations are skipped.
func (f *frameConn) handleIAC() error {
	cmd, err := f.br.ReadByte()
	if err != nil {
		return err
	}
	switch cmd {
	case iac:
		// Literal 0xFF — should not occur in the text phase; drop it.
		return nil
	case doo, dont, will, wont:
		opt, err := f.br.ReadByte()
		if err != nil {
			return err
		}
		var resp [3]byte
		resp[0] = iac
		switch cmd {
		case doo:
			resp[1] = wont
		case will:
			resp[1] = dont
		default: // DONT/WONT — acknowledge by staying silent
			return nil
		}
		resp[2] = opt
		obs.Trace(f.log, "qconn-telnet", ">>", resp[:])
		_, err = f.raw.Write(resp[:])
		return err
	case sb:
		// Skip until IAC SE.
		for {
			b, err := f.br.ReadByte()
			if err != nil {
				return err
			}
			if b == iac {
				n, err := f.br.ReadByte()
				if err != nil {
					return err
				}
				if n == se {
					return nil
				}
			}
		}
	default:
		return nil
	}
}

// readUntilPrompt reads bytes until a service prompt terminates the buffer,
// returning everything before the prompt. This is the core request/response
// synchronization primitive (analogous to Net::Telnet's cmd()).
func (f *frameConn) readUntilPrompt() (string, error) {
	var sb strings.Builder
	scan := 0 // index from which to re-test the prompt regexp
	for {
		b, err := f.readByte()
		if err != nil {
			if err == io.EOF && sb.Len() > 0 {
				return sb.String(), nil
			}
			return sb.String(), err
		}
		sb.WriteByte(b)
		// Only bother running the regexp when we just saw a '>' (prompt end).
		if b == '>' || b == ' ' || b == '\n' {
			s := sb.String()
			if loc := promptRe.FindStringIndex(s[scan:]); loc != nil {
				// qconn prompts are "<qconn-SVC> " (trailing space). The regex
				// matches at '>' before the space is read, so consume any
				// already-buffered trailing whitespace now to stop it leaking
				// into the next response.
				for f.br.Buffered() > 0 {
					p, _ := f.br.Peek(1)
					if len(p) == 1 && (p[0] == ' ' || p[0] == '\t') {
						_, _ = f.br.ReadByte()
					} else {
						break
					}
				}
				return strings.TrimLeft(s[:scan+loc[0]], " "), nil
			}
			if sb.Len() > 256 {
				scan = sb.Len() - 64 // bound rescans on long output
			}
		}
	}
}

// readN reads exactly n raw bytes (used for binary file-service payloads).
func (f *frameConn) readN(n int) ([]byte, error) {
	buf := make([]byte, n)
	f.deadline()
	_, err := io.ReadFull(f.br, buf)
	if err == nil {
		obs.Trace(f.log, "qconn", "<<", buf)
	}
	return buf, err
}

// readResponseLine reads a single LF-terminated line (CR-trimmed). Used where a
// command yields one status line before binary data (file read/write).
func (f *frameConn) readResponseLine() (string, error) {
	var sb strings.Builder
	for {
		b, err := f.readByte()
		if err != nil {
			return sb.String(), err
		}
		if b == '\n' {
			// TrimLeft guards against a leaked prompt trailing-space prefix.
			return strings.TrimLeft(strings.TrimRight(sb.String(), "\r"), " "), nil
		}
		sb.WriteByte(b)
	}
}

func (f *frameConn) close() error { return f.raw.Close() }

// handshake consumes the greeting up to the first service prompt.
func (f *frameConn) handshake() error {
	_, err := f.readUntilPrompt()
	return err
}

// readUntilIdle reads bytes until no data arrives for idle, or EOF. Used for the
// launcher "run" mode, which streams a child's output and never emits a prompt.
// IAC sequences and the benign linemode-noise line are stripped from the result.
func (f *frameConn) readUntilIdle(idle time.Duration) []byte {
	var raw []byte
	tmp := make([]byte, 8192)
	for {
		_ = f.raw.SetReadDeadline(time.Now().Add(idle))
		n, err := f.br.Read(tmp)
		if n > 0 {
			raw = append(raw, tmp[:n]...)
		}
		if err != nil {
			break // idle timeout or EOF
		}
	}
	obs.Trace(f.log, "qconn", "<<", raw)
	return stripIAC(raw)
}

// stripIAC removes telnet IAC command sequences (0xFF + 2 bytes; SB...SE) from a
// raw byte slice, returning just the application data.
func stripIAC(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		if b[i] != iac {
			out = append(out, b[i])
			continue
		}
		if i+1 >= len(b) {
			break
		}
		switch b[i+1] {
		case doo, dont, will, wont:
			i += 2 // skip IAC + cmd + option
		case sb:
			i += 2
			for i < len(b) && !(b[i] == iac && i+1 < len(b) && b[i+1] == se) {
				i++
			}
			i++ // skip the SE
		default:
			i++ // IAC + single command byte
		}
	}
	return out
}

// benignNoise is the informational line qconn emits once after connect because
// the client refuses telnet LINEMODE/ECHO negotiation. It can trail into the
// first command's response block, so it is filtered everywhere.
const benignNoise = "error linemode-or-echo-not-supported"

func isNoise(ln string) bool {
	return strings.TrimSpace(ln) == "" || strings.TrimSpace(ln) == benignNoise
}

// firstLine returns the first meaningful line of a response block.
func firstLine(s string) string {
	for _, ln := range strings.Split(s, "\n") {
		ln = strings.TrimRight(ln, "\r")
		if !isNoise(ln) {
			return ln
		}
	}
	return ""
}

// splitLines returns the trimmed, meaningful lines of a response block.
func splitLines(s string) []string {
	var out []string
	for _, ln := range strings.Split(s, "\n") {
		ln = strings.TrimRight(ln, "\r")
		if !isNoise(ln) {
			out = append(out, ln)
		}
	}
	return out
}

// stripLeadingNoise removes leading empty/benign-noise lines from a response
// block, preserving the remainder (including its internal newlines) verbatim.
func stripLeadingNoise(s string) string {
	for {
		nl := strings.IndexByte(s, '\n')
		if nl < 0 {
			return s
		}
		if isNoise(s[:nl]) {
			s = s[nl+1:]
			continue
		}
		return s
	}
}

// protoErr wraps an unexpected server response for clearer diagnostics.
func protoErr(op, got string) error {
	return fmt.Errorf("qconn %s: unexpected response %q", op, strings.TrimSpace(got))
}
