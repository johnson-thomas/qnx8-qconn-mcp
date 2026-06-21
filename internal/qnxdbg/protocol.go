// Package qnxdbg implements the QNX "pdebug" debug-server protocol (the
// proprietary protocol the QNX-patched gdb speaks to the pdebug agent), as used
// for full source/machine-level debugging on QNX Neutrino targets.
//
// This is NOT the GDB Remote Serial Protocol. pdebug frames messages with the
// 0x7e ('~') delimiter and a one's-complement checksum, and carries fixed
// "DSMSG" command/response structures. The framing, checksum, escaping, counter
// scheme and handshake were derived from Mandiant's rpdebug_qnx (rpdbg.py) and
// the QNX dsmsgs.h message layout, and validated against a real QNX 8 target.
package qnxdbg

// Frame bytes.
const (
	frameChar = 0x7e // '~' packet delimiter
	escChar   = 0x7d // '}' escape prefix; escaped byte is original ^ 0x20
	escXor    = 0x20
)

// Channel identifiers (the high byte of the 16-bit message counter == channel).
const (
	chanReset = 0x00
	chanDebug = 0x01
	chanText  = 0x02
	chanNak   = 0xff
)

// DSMSG command codes (host -> target). Values from rpdbg.py / dsmsgs.h.
const (
	cmdConnect    = 0
	cmdDisconnect = 1
	cmdSelect     = 2
	cmdMapinfo    = 3
	cmdLoad       = 4
	cmdAttach     = 5
	cmdDetach     = 6
	cmdKill       = 7
	cmdStop       = 8
	cmdMemrd      = 9
	cmdMemwr      = 10
	cmdRegrd      = 11
	cmdRegwr      = 12
	cmdRun        = 13
	cmdBrk        = 14
	cmdFileopen   = 15
	cmdFilerd     = 16
	cmdFilewr     = 17
	cmdFileclose  = 18
	cmdPidlist    = 19
	cmdCwd        = 20
	cmdEnv        = 21
	cmdBase       = 22
	cmdProtover   = 23
	cmdHandlesig  = 24
	cmdCPUInfo    = 25
	cmdTIDNames   = 26
	cmdProcMap    = 27 // 0x1b: per-process memory map (segment load addresses)
)

// DSMSG response codes (target -> host); appear as payload[0] of a reply.
const (
	respErr      = 0x20
	respOK       = 0x21
	respOKStatus = 0x22
	respOKData   = 0x23
	// Notification (asynchronous stop event) message type.
	respNotify = 0x40
)

// checksum is the protocol's one's-complement of the byte sum.
func checksum(b []byte) byte {
	var s byte
	for _, c := range b {
		s += c
	}
	return 0xff - s
}

// escape replaces 0x7e/0x7d with their two-byte escaped form.
func escape(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for _, c := range b {
		if c == frameChar || c == escChar {
			out = append(out, escChar, c^escXor)
		} else {
			out = append(out, c)
		}
	}
	return out
}

// unescape reverses escape on received frame content.
func unescape(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		if b[i] == escChar && i+1 < len(b) {
			i++
			out = append(out, b[i]^escXor)
		} else {
			out = append(out, b[i])
		}
	}
	return out
}

// frame wraps a payload into a complete on-wire frame:
//
//	0x7e | escape(payload) | escape(checksum(payload)) | 0x7e
func frame(payload []byte) []byte {
	cs := checksum(payload)
	out := make([]byte, 0, len(payload)+4)
	out = append(out, frameChar)
	out = append(out, escape(payload)...)
	out = append(out, escape([]byte{cs})...)
	out = append(out, frameChar)
	return out
}
