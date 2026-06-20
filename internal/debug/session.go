package debug

import (
	"fmt"
	"strconv"
	"strings"
)

// Session is a high-level debug session over an RSP connection to pdebug.
type Session struct {
	rsp *RSP
	pid int
}

// NewSession wraps an RSP connection and performs the initial feature exchange.
func NewSession(rsp *RSP) (*Session, error) {
	s := &Session{rsp: rsp}
	// Best-effort feature negotiation; pdebug tolerates unknown features.
	_, _ = rsp.Command("qSupported:multiprocess+;vContSupported+")
	return s, nil
}

// Close detaches (best effort) and closes the connection.
func (s *Session) Close() error {
	if s.pid != 0 {
		_, _ = s.rsp.Command("D")
	}
	return s.rsp.Close()
}

// StopReply is a parsed S/T stop packet.
type StopReply struct {
	Signal int               `json:"signal"`
	Thread string            `json:"thread,omitempty"`
	Values map[string]string `json:"values,omitempty"`
	Raw    string            `json:"raw"`
}

// Attach attaches to an existing pid via vAttach (falling back to a plain query
// if the stub lacks vAttach support).
func (s *Session) Attach(pid int) (*StopReply, error) {
	resp, err := s.rsp.Command(fmt.Sprintf("vAttach;%x", pid))
	if err != nil {
		return nil, err
	}
	if isError(resp) || resp == "" {
		// Fallback: select process/thread then query status.
		_, _ = s.rsp.Command(fmt.Sprintf("Hg p%x.0", pid))
		resp, err = s.rsp.Command("?")
		if err != nil {
			return nil, err
		}
	}
	s.pid = pid
	return parseStop(resp), nil
}

// LaunchStatus queries the current stop status (used right after a launch).
func (s *Session) Status() (*StopReply, error) {
	resp, err := s.rsp.Command("?")
	if err != nil {
		return nil, err
	}
	return parseStop(resp), nil
}

// Detach detaches from the inferior, leaving it running.
func (s *Session) Detach() error {
	resp, err := s.rsp.Command("D")
	if err != nil {
		return err
	}
	s.pid = 0
	if isError(resp) {
		return fmt.Errorf("detach: %s", resp)
	}
	return nil
}

// KillInferior terminates the inferior process.
func (s *Session) KillInferior() error {
	return s.rsp.SendPacket("k")
}

// Continue resumes execution and waits for the next stop event.
func (s *Session) Continue() (*StopReply, error) {
	resp, err := s.rsp.Command("c")
	if err != nil {
		return nil, err
	}
	return parseStop(resp), nil
}

// Step single-steps one instruction.
func (s *Session) Step() (*StopReply, error) {
	resp, err := s.rsp.Command("s")
	if err != nil {
		return nil, err
	}
	return parseStop(resp), nil
}

// ReadRegisters returns the raw register block hex (`g`). Decoding into named
// registers is architecture-specific and left to the caller/UI; the hex is the
// authoritative value.
func (s *Session) ReadRegisters() (string, error) {
	resp, err := s.rsp.Command("g")
	if err != nil {
		return "", err
	}
	if isError(resp) {
		return "", fmt.Errorf("read registers: %s", resp)
	}
	return resp, nil
}

// ReadMemory reads len bytes at addr (`m addr,len`).
func (s *Session) ReadMemory(addr uint64, length int) ([]byte, error) {
	resp, err := s.rsp.Command(fmt.Sprintf("m%x,%x", addr, length))
	if err != nil {
		return nil, err
	}
	if isError(resp) {
		return nil, fmt.Errorf("read memory @%#x: %s", addr, resp)
	}
	return hexDecode(resp)
}

// WriteMemory writes data at addr (`M addr,len:hex`).
func (s *Session) WriteMemory(addr uint64, data []byte) error {
	resp, err := s.rsp.Command(fmt.Sprintf("M%x,%x:%s", addr, len(data), hexEncode(data)))
	if err != nil {
		return err
	}
	if resp != "OK" {
		return fmt.Errorf("write memory @%#x: %s", addr, resp)
	}
	return nil
}

// BreakpointKind selects software (0) or hardware (1) breakpoints per RSP Z/z.
type BreakpointKind int

const (
	SWBreak BreakpointKind = 0
	HWBreak BreakpointKind = 1
)

// SetBreakpoint inserts a breakpoint at addr. kindBytes is the architecture
// breakpoint length (e.g. 1 on x86, 4 on ARM); 0 lets the stub choose.
func (s *Session) SetBreakpoint(addr uint64, kind BreakpointKind, kindBytes int) error {
	resp, err := s.rsp.Command(fmt.Sprintf("Z%d,%x,%d", kind, addr, kindBytes))
	if err != nil {
		return err
	}
	if resp != "OK" {
		return fmt.Errorf("set breakpoint @%#x: %s", addr, resp)
	}
	return nil
}

// ClearBreakpoint removes a breakpoint at addr.
func (s *Session) ClearBreakpoint(addr uint64, kind BreakpointKind, kindBytes int) error {
	resp, err := s.rsp.Command(fmt.Sprintf("z%d,%x,%d", kind, addr, kindBytes))
	if err != nil {
		return err
	}
	if resp != "OK" {
		return fmt.Errorf("clear breakpoint @%#x: %s", addr, resp)
	}
	return nil
}

// ListThreads returns the inferior's thread IDs via qfThreadInfo/qsThreadInfo.
func (s *Session) ListThreads() ([]string, error) {
	var ids []string
	resp, err := s.rsp.Command("qfThreadInfo")
	if err != nil {
		return nil, err
	}
	for {
		if resp == "" || resp == "l" || isError(resp) {
			break
		}
		if strings.HasPrefix(resp, "m") {
			for _, t := range strings.Split(resp[1:], ",") {
				if t != "" {
					ids = append(ids, t)
				}
			}
		}
		resp, err = s.rsp.Command("qsThreadInfo")
		if err != nil {
			return ids, err
		}
	}
	return ids, nil
}

// --- parsing helpers -----------------------------------------------------

func isError(resp string) bool {
	return len(resp) == 3 && resp[0] == 'E'
}

// parseStop parses S<sig> / T<sig><k:v;...> / W<code> (exit) / X<sig> packets.
func parseStop(resp string) *StopReply {
	sr := &StopReply{Raw: resp, Values: map[string]string{}}
	if resp == "" {
		return sr
	}
	switch resp[0] {
	case 'S', 'T', 'X', 'W':
		if len(resp) >= 3 {
			if v, err := strconv.ParseInt(resp[1:3], 16, 32); err == nil {
				sr.Signal = int(v)
			}
		}
		if resp[0] == 'T' {
			for _, kv := range strings.Split(resp[3:], ";") {
				if kv == "" {
					continue
				}
				if k, v, ok := strings.Cut(kv, ":"); ok {
					if k == "thread" {
						sr.Thread = v
					}
					sr.Values[k] = v
				}
			}
		}
	}
	return sr
}
