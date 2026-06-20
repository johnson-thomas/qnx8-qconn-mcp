package mockqnx

import (
	"bufio"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// serveMockRSP implements a minimal GDB Remote Serial Protocol stub standing in
// for pdebug: feature exchange, attach/status, continue/step, registers, memory
// read/write, breakpoints, thread listing and detach. One inferior, fixed
// registers — enough to validate the RSP codec and debug tool wiring.
func serveMockRSP(port int) error {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return err
	}
	defer ln.Close()
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go handleRSP(conn)
	}
}

func handleRSP(conn net.Conn) {
	defer conn.Close()
	br := bufio.NewReader(conn)
	for {
		payload, ok := rspRead(br, conn)
		if !ok {
			return
		}
		resp := rspHandle(payload)
		if resp == closeConn {
			return
		}
		rspWrite(conn, resp)
	}
}

const closeConn = "\x00__close__"

func rspHandle(p string) string {
	switch {
	case p == "":
		return ""
	case strings.HasPrefix(p, "qSupported"):
		return "PacketSize=4000;multiprocess+;vContSupported+;QStartNoAckMode+"
	case p == "QStartNoAckMode":
		return "OK"
	case strings.HasPrefix(p, "vAttach"):
		return "T05thread:1;"
	case p == "?":
		return "T05thread:1;"
	case p == "c", p == "s":
		return "T05thread:1;"
	case p == "g":
		var b strings.Builder
		for i := 0; i < 16; i++ {
			for j := 0; j < 8; j++ {
				fmt.Fprintf(&b, "%02x", (i*8+j)&0xff)
			}
		}
		return b.String()
	case strings.HasPrefix(p, "m"):
		body := p[1:]
		comma := strings.IndexByte(body, ',')
		if comma < 0 {
			return "E01"
		}
		addr, _ := strconv.ParseUint(body[:comma], 16, 64)
		n, _ := strconv.ParseUint(body[comma+1:], 16, 64)
		var b strings.Builder
		for i := uint64(0); i < n; i++ {
			fmt.Fprintf(&b, "%02x", (addr+i)&0xff)
		}
		return b.String()
	case strings.HasPrefix(p, "M"):
		return "OK"
	case strings.HasPrefix(p, "Z"), strings.HasPrefix(p, "z"):
		return "OK"
	case p == "qfThreadInfo":
		return "m1"
	case p == "qsThreadInfo":
		return "l"
	case strings.HasPrefix(p, "Hg"), strings.HasPrefix(p, "Hc"):
		return "OK"
	case p == "D":
		return "OK"
	case p == "k":
		return closeConn
	default:
		return ""
	}
}

func rspRead(br *bufio.Reader, conn net.Conn) (string, bool) {
	for {
		b, err := br.ReadByte()
		if err != nil {
			return "", false
		}
		if b == '$' {
			break
		}
	}
	var raw []byte
	for {
		b, err := br.ReadByte()
		if err != nil {
			return "", false
		}
		if b == '#' {
			break
		}
		raw = append(raw, b)
	}
	if _, err := br.Discard(2); err != nil {
		return "", false
	}
	_, _ = conn.Write([]byte{'+'})
	return string(rspDecode(raw)), true
}

func rspWrite(conn net.Conn, payload string) {
	body := rspEscape([]byte(payload))
	var sum byte
	for _, c := range body {
		sum += c
	}
	frame := append([]byte{'$'}, body...)
	frame = append(frame, '#', hexDigit(sum>>4), hexDigit(sum&0xf))
	_, _ = conn.Write(frame)
}

func rspEscape(p []byte) []byte {
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

func rspDecode(p []byte) []byte {
	out := make([]byte, 0, len(p))
	for i := 0; i < len(p); i++ {
		if p[i] == '}' && i+1 < len(p) {
			i++
			out = append(out, p[i]^0x20)
		} else {
			out = append(out, p[i])
		}
	}
	return out
}

func hexDigit(b byte) byte {
	const h = "0123456789abcdef"
	return h[b&0xf]
}
