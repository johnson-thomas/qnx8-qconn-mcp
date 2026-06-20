// Package mockqnx is a protocol-faithful mock of a QNX qconn daemon for local
// development and CI of the qconn client and MCP server without QNX hardware.
//
// It implements the broker greeting and the launcher/file/cntl services, serves
// canned pidin output and a synthetic /proc/<pid>/as memory device, and (when
// asked to run "pdebug <port>") starts a minimal GDB-RSP responder so the debug
// path is testable end to end. It is a TEST DOUBLE — refine against real
// hardware using qconn-proxy + trace logs.
package mockqnx

import (
	"bufio"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
)

// ListenAndServe listens on addr and serves mock qconn connections until the
// listener errors. It returns the bound *net.TCPListener-backed Listener via the
// started callback (may be nil) for tests that need the chosen port.
func ListenAndServe(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return Accept(ln)
}

// Accept serves connections on an existing listener (handy for tests using
// net.Listen("tcp", "127.0.0.1:0")).
func Accept(ln net.Listener) error {
	for {
		c, err := ln.Accept()
		if err != nil {
			return err
		}
		go Serve(c)
	}
}

type session struct {
	c     net.Conn
	br    *bufio.Reader
	svc   string
	fds   map[int64]*vfile
	files map[string][]byte // persistent content keyed by path
	nfd   int64
}

type vfile struct {
	path   string
	isProc bool
}

// Serve handles a single mock qconn connection.
func Serve(c net.Conn) {
	defer c.Close()
	s := &session{c: c, br: bufio.NewReader(c), svc: "broker", fds: map[int64]*vfile{}, files: map[string][]byte{}}

	// Real qconn greeting order: banner, broker prompt, then the
	// linemode/echo error line emitted after the client refuses telnet option
	// negotiation. There is no further prompt until the first command response.
	s.write("QCONN\r\n")
	s.prompt()
	s.write("error linemode-or-echo-not-supported\r\n")

	for {
		line, err := s.br.ReadString('\n')
		if err != nil {
			return
		}
		if !s.dispatch(strings.TrimRight(line, "\r\n")) {
			return
		}
	}
}

func (s *session) write(str string) { _, _ = s.c.Write([]byte(str)) }
func (s *session) prompt()          { s.write("<qconn-" + s.svc + ">") }

func (s *session) dispatch(cmd string) bool {
	switch s.svc {
	case "broker":
		return s.broker(cmd)
	case "launcher":
		return s.launcher(cmd)
	case "file":
		return s.file(cmd)
	case "cntl":
		return s.cntl(cmd)
	}
	return false
}

func (s *session) broker(cmd string) bool {
	switch {
	case cmd == "quit":
		return false
	case cmd == "versions ?":
		s.write("versions: broker/256 launcher/256 file/256 cntl/256 sinfo/256\r\n")
	case strings.HasPrefix(cmd, "versions "):
		s.write("256\r\n")
	case cmd == "info":
		s.write(mockInfo + "\r\n")
	case cmd == "service launcher":
		s.svc = "launcher"
		s.write("OK\r\n")
	case cmd == "service file":
		s.svc = "file"
		s.write("OK\r\n")
	case cmd == "service cntl":
		s.svc = "cntl"
		s.write("OK\r\n")
	default:
		s.write("error unknown-command\r\n")
	}
	s.prompt()
	return true
}

var startRe = regexp.MustCompile(`^start/flags\s+(\S+)\s+(\S+)\s+(\S+)\s+-c\s+"(.*)"\s*$`)

func (s *session) launcher(cmd string) bool {
	switch {
	case cmd == "done" || cmd == "quit":
		s.svc = "broker"
	case cmd == "chdir /":
		s.write("OK\r\n")
	case strings.HasPrefix(cmd, "start/flags"):
		// Real qconn "run" is one-shot: stream "OK <pid>" + child output, emit no
		// prompt, and the launcher session ends. We close the connection so the
		// client's idle-read returns promptly (real qconn just goes silent).
		s.write(fmt.Sprintf("OK %d\r\n", 8300))
		if m := startRe.FindStringSubmatch(cmd); m != nil {
			s.write(runShell(m[4]))
		}
		return false
	default:
		s.write("error unknown-command\r\n")
	}
	s.prompt()
	return true
}

func (s *session) cntl(cmd string) bool {
	switch {
	case cmd == "done" || cmd == "quit":
		s.svc = "broker"
	case strings.HasPrefix(cmd, "kill "):
		s.write("ok\r\n")
	default:
		s.write("error unknown-command\r\n")
	}
	s.prompt()
	return true
}

var openRe = regexp.MustCompile(`^o:"([^"]+)":([0-9a-fA-F]+):([0-9a-fA-F]+)$`)
var rwRe = regexp.MustCompile(`^([rw]):([0-9a-fA-F]+):([0-9a-fA-F]+):([0-9a-fA-F]+)$`)

func (s *session) file(cmd string) bool {
	switch {
	case cmd == "q" || cmd == "done" || cmd == "quit":
		s.svc = "broker"
		s.prompt()
		return true
	case strings.HasPrefix(cmd, "o:"):
		m := openRe.FindStringSubmatch(cmd)
		if m == nil {
			s.write("error bad-open\r\n")
			break
		}
		path := m[1]
		fd := s.nfd + 3
		s.nfd++
		vf := &vfile{path: path}
		var size int64
		mode := int64(0100644)
		switch {
		case strings.HasPrefix(path, "/proc/") && strings.HasSuffix(path, "/as"):
			vf.isProc = true
			size = 0x7fffffff
		case strings.HasPrefix(path, "/proc/"):
			s.files[path] = []byte("synthetic proc node\n")
			size = int64(len(s.files[path]))
		default:
			size = int64(len(s.files[path])) // 0 for a new file, len for existing
		}
		s.fds[fd] = vf
		s.write(fmt.Sprintf("o:%x:%x:%x:\"%s\"\r\n", fd, mode, size, path))
	case strings.HasPrefix(cmd, "c:"), strings.HasPrefix(cmd, "d:"), strings.HasPrefix(cmd, "a:"):
		s.write("o\r\n")
	case strings.HasPrefix(cmd, "r:"):
		m := rwRe.FindStringSubmatch(cmd)
		if m == nil {
			s.write("error bad-read\r\n")
			break
		}
		fd, _ := strconv.ParseInt(m[2], 16, 64)
		off, _ := strconv.ParseInt(m[3], 16, 64)
		n, _ := strconv.ParseInt(m[4], 16, 64)
		data := s.readVfile(fd, off, int(n))
		s.write(fmt.Sprintf("o:%x:0:0\r\n", len(data)))
		_, _ = s.c.Write(data)
	case strings.HasPrefix(cmd, "w:"):
		m := rwRe.FindStringSubmatch(cmd)
		if m == nil {
			s.write("error bad-write\r\n")
			break
		}
		fd, _ := strconv.ParseInt(m[2], 16, 64)
		off, _ := strconv.ParseInt(m[3], 16, 64)
		n, _ := strconv.ParseInt(m[4], 16, 64)
		buf := make([]byte, n)
		_, _ = readFull(s.br, buf)
		s.storeVfile(fd, off, buf)
		s.write(fmt.Sprintf("o:%x:0\r\n", n))
	default:
		s.write("error unknown-command\r\n")
	}
	s.prompt()
	return true
}

func (s *session) readVfile(fd, off int64, n int) []byte {
	vf := s.fds[fd]
	if vf == nil {
		return nil
	}
	if vf.isProc {
		out := make([]byte, n)
		for i := range out {
			out[i] = byte((off + int64(i)) & 0xff)
		}
		return out
	}
	buf := s.files[vf.path]
	if off >= int64(len(buf)) {
		return nil
	}
	end := off + int64(n)
	if end > int64(len(buf)) {
		end = int64(len(buf))
	}
	return buf[off:end]
}

func (s *session) storeVfile(fd, off int64, data []byte) {
	vf := s.fds[fd]
	if vf == nil || vf.isProc {
		return
	}
	buf := s.files[vf.path]
	need := int(off) + len(data)
	if need > len(buf) {
		grown := make([]byte, need)
		copy(grown, buf)
		buf = grown
	}
	copy(buf[off:], data)
	s.files[vf.path] = buf
}

func readFull(br *bufio.Reader, buf []byte) (int, error) {
	got := 0
	for got < len(buf) {
		n, err := br.Read(buf[got:])
		got += n
		if err != nil {
			return got, err
		}
	}
	return got, nil
}
