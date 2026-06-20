package qnxdbg

import (
	"encoding/binary"
	"fmt"
	"strings"
)

// cmdNames maps host->target command codes to names (for protocol analysis).
var cmdNames = map[byte]string{
	cmdConnect: "Connect", cmdDisconnect: "Disconnect", cmdSelect: "Select",
	cmdMapinfo: "Mapinfo", cmdLoad: "Load", cmdAttach: "Attach", cmdDetach: "Detach",
	cmdKill: "Kill", cmdStop: "Stop", cmdMemrd: "Memrd", cmdMemwr: "Memwr",
	cmdRegrd: "Regrd", cmdRegwr: "Regwr", cmdRun: "Run", cmdBrk: "Brk",
	cmdFileopen: "Fileopen", cmdFilerd: "Filerd", cmdFilewr: "Filewr",
	cmdFileclose: "Fileclose", cmdPidlist: "Pidlist", cmdCwd: "Cwd", cmdEnv: "Env",
	cmdBase: "Base", cmdProtover: "Protover", cmdHandlesig: "Handlesig",
	cmdCPUInfo: "CPUInfo", cmdTIDNames: "TIDNames",
}

var respNames = map[byte]string{
	respErr: "ERR", respOK: "OK", respOKStatus: "OKStatus", respOKData: "OKData",
	respNotify: "NOTIFY",
}

// CmdName returns a human name for a command code.
func CmdName(b byte) string {
	if n, ok := cmdNames[b]; ok {
		return n
	}
	return fmt.Sprintf("cmd?0x%02x", b)
}

// Annotate decodes a DSMSG frame payload (header+body, no checksum) into a
// human-readable description for protocol analysis. Direction is inferred from
// the first byte: command codes are < 0x20, responses are >= 0x20.
func Annotate(payload []byte) string {
	if len(payload) < 4 {
		return fmt.Sprintf("control/short %x", payload)
	}
	code, sub, mid, ch := payload[0], payload[1], payload[2], payload[3]
	body := payload[4:]
	var name string
	isResp := code >= 0x20
	if isResp {
		if n, ok := respNames[code]; ok {
			name = "<-" + n
		} else {
			name = fmt.Sprintf("<-resp?0x%02x", code)
		}
	} else {
		name = "->" + CmdName(code)
	}
	desc := fmt.Sprintf("%s sub=%d mid=%d ch=%d", name, sub, mid, ch)
	if d := decodeBody(code, isResp, body); d != "" {
		desc += "  " + d
	}
	if len(body) > 0 {
		desc += fmt.Sprintf("  body=%x", body)
	}
	return desc
}

func decodeBody(code byte, isResp bool, b []byte) string {
	if isResp {
		return ""
	}
	switch code {
	case cmdAttach, cmdDetach, cmdKill:
		if len(b) >= 4 {
			return fmt.Sprintf("pid=%d", binary.LittleEndian.Uint32(b))
		}
	case cmdSelect:
		if len(b) >= 8 {
			return fmt.Sprintf("pid=%d tid=%d", binary.LittleEndian.Uint32(b), binary.LittleEndian.Uint32(b[4:]))
		}
	case cmdMemrd, cmdMemwr:
		if len(b) >= 12 {
			addr := binary.LittleEndian.Uint64(b[4:12])
			s := fmt.Sprintf("addr=%#x", addr)
			if code == cmdMemrd && len(b) >= 14 {
				s += fmt.Sprintf(" size=%d", binary.LittleEndian.Uint16(b[12:14]))
			}
			if code == cmdMemwr {
				s += fmt.Sprintf(" datalen=%d", len(b)-12)
			}
			return s
		}
	case cmdRegrd:
		if len(b) >= 4 {
			return fmt.Sprintf("off=%#x size=%d", binary.LittleEndian.Uint16(b), binary.LittleEndian.Uint16(b[2:]))
		}
	case cmdRegwr:
		if len(b) >= 2 {
			return fmt.Sprintf("off=%#x datalen=%d", binary.LittleEndian.Uint16(b), len(b)-2)
		}
	case cmdBrk:
		// Verified layout: subcmd=type(_DEBUG_BREAK_*); body = size:int32
		// (0=set, -1=remove, 1..8=watchpoint span) then addr:uint64.
		if len(b) >= 12 {
			size := int32(binary.LittleEndian.Uint32(b[0:4]))
			addr := binary.LittleEndian.Uint64(b[4:12])
			op := "set"
			if size < 0 {
				op = "remove"
			}
			return fmt.Sprintf("BRK %s addr=%#x size=%d", op, addr, size)
		}
		return "BRK"
	}
	return ""
}

// FrameScanner extracts complete DSMSG frames from a byte stream that may split
// or coalesce frames across reads.
type FrameScanner struct {
	buf []byte
}

// Feed appends data and returns the verified payloads (header+body, checksum
// stripped) of any complete frames now available, plus their raw on-wire bytes.
func (s *FrameScanner) Feed(data []byte) (payloads [][]byte, raws [][]byte) {
	s.buf = append(s.buf, data...)
	for {
		// Skip leading delimiters.
		i := 0
		for i < len(s.buf) && s.buf[i] == frameChar {
			i++
		}
		if i >= len(s.buf) {
			s.buf = s.buf[:0]
			return
		}
		// Find closing delimiter.
		j := i
		for j < len(s.buf) && s.buf[j] != frameChar {
			j++
		}
		if j >= len(s.buf) {
			s.buf = append([]byte(nil), s.buf[i:]...) // keep partial frame
			return
		}
		content := s.buf[i:j]
		raw := make([]byte, 0, len(content)+2)
		raw = append(raw, frameChar)
		raw = append(raw, content...)
		raw = append(raw, frameChar)
		dec := unescape(content)
		if len(dec) >= 1 {
			payloads = append(payloads, dec[:len(dec)-1]) // strip checksum
			raws = append(raws, raw)
		}
		s.buf = append([]byte(nil), s.buf[j:]...) // keep closing delim as next start
	}
}

// HexDump renders bytes compactly (truncated) for logs.
func HexDump(b []byte) string {
	const max = 256
	t := b
	suffix := ""
	if len(t) > max {
		t = t[:max]
		suffix = "..."
	}
	var sb strings.Builder
	for _, c := range t {
		fmt.Fprintf(&sb, "%02x", c)
	}
	return sb.String() + suffix
}
