package mockqnx

import (
	"bufio"
	"encoding/binary"
	"net"
)

// ServeDSMSG handles one mock pdebug (QNX DSMSG) connection: it implements the
// connect handshake and replies to attach/select/regrd/memrd/regwr/memwr/detach
// well enough to exercise the qnxdbg client. Register/memory data is synthetic
// (memory byte at address A == A & 0xff; pc register == 0x4000).
func ServeDSMSG(conn net.Conn) {
	defer conn.Close()
	br := bufio.NewReader(conn)

	const fc = 0x7e
	cksum := func(b []byte) byte {
		var s byte
		for _, c := range b {
			s += c
		}
		return 0xff - s
	}
	esc := func(b []byte) []byte {
		out := make([]byte, 0, len(b))
		for _, c := range b {
			if c == 0x7e || c == 0x7d {
				out = append(out, 0x7d, c^0x20)
			} else {
				out = append(out, c)
			}
		}
		return out
	}
	writeFrame := func(payload []byte) {
		f := []byte{fc}
		f = append(f, esc(payload)...)
		f = append(f, esc([]byte{cksum(payload)})...)
		f = append(f, fc)
		_, _ = conn.Write(f)
	}
	readFrame := func() ([]byte, bool) {
		var first byte
		for {
			b, err := br.ReadByte()
			if err != nil {
				return nil, false
			}
			if b != fc {
				first = b
				break
			}
		}
		content := []byte{first}
		for {
			b, err := br.ReadByte()
			if err != nil {
				return nil, false
			}
			if b == fc {
				break
			}
			content = append(content, b)
		}
		// unescape
		out := make([]byte, 0, len(content))
		for i := 0; i < len(content); i++ {
			if content[i] == 0x7d && i+1 < len(content) {
				i++
				out = append(out, content[i]^0x20)
			} else {
				out = append(out, content[i])
			}
		}
		if len(out) < 1 {
			return nil, false
		}
		return out[:len(out)-1], true // strip checksum
	}
	reply := func(code, mid byte, body []byte) {
		p := append([]byte{code, 0x00, mid, 0x01}, body...)
		writeFrame(p)
	}

	// State tracked to emulate breakpoint/continue: the attached pid and the
	// most recently set breakpoint address (the synthetic "stop" PC).
	var attachedPID uint32 = 0x1000
	var brkAddr uint64

	// Handshake: send reset, expect echo, then Connect + Protover.
	writeFrame([]byte{0x00})
	for {
		p, ok := readFrame()
		if !ok {
			return
		}
		if len(p) < 4 {
			continue // control/echo frame
		}
		cmd, mid := p[0], p[2]
		switch cmd {
		case 0: // Connect
			reply(0x22, mid, []byte{0x20, 0x04, 0x00, 0x00})
		case 23: // Protover
			reply(0x22, mid, []byte{0x01, 0x03, 0x00, 0x00})
		case 21: // Env (stage argv/env before Load) -> ok
			reply(0x21, mid, nil)
		case 4: // Load -> okdata with pid+tid (process loaded, stopped at entry)
			attachedPID = 0x2000
			body := append(appendLE32(nil, attachedPID), appendLE32(nil, 1)...)
			body = append(body, []byte("mockproc")...)
			reply(0x23, mid, body)
		case 5: // Attach -> okdata + a fake name
			if body := p[4:]; len(body) >= 4 {
				attachedPID = binary.LittleEndian.Uint32(body)
			}
			reply(0x23, mid, append(make([]byte, 8), []byte("mockproc")...))
		case 2: // Select -> ok
			reply(0x21, mid, nil)
		case 11: // Regrd -> 0x110 bytes, pc(0x100)=0x4000
			regs := make([]byte, 0x110)
			binary.LittleEndian.PutUint64(regs[0x100:], 0x4000)
			reply(0x23, mid, regs)
		case 12: // Regwr -> ok
			reply(0x21, mid, nil)
		case 9: // Memrd: body = spare0[4] addr[8] size[2] spare[6]
			body := p[4:]
			var addr uint64
			var size uint16
			if len(body) >= 14 {
				addr = binary.LittleEndian.Uint64(body[4:12])
				size = binary.LittleEndian.Uint16(body[12:14])
			}
			data := make([]byte, size)
			for i := range data {
				data[i] = byte((addr + uint64(i)) & 0xff)
			}
			reply(0x23, mid, data)
		case 10: // Memwr -> ok
			reply(0x21, mid, nil)
		case 14: // Brk: body = size:int32 (0=set, -1=remove) addr:uint64
			if body := p[4:]; len(body) >= 12 {
				size := int32(binary.LittleEndian.Uint32(body[0:4]))
				if size < 0 {
					brkAddr = 0 // removed
				} else {
					brkAddr = binary.LittleEndian.Uint64(body[4:12])
				}
			}
			reply(0x21, mid, nil)
		case 13: // Run -> OK, then an async stop notification (0x40)
			reply(0x21, mid, nil)
			// Notify body: pid[4] tid[4] then a status block whose
			// register-area ip (offset matching ReadRegisters 0x100) is the
			// breakpoint address. Here we just include pid/tid + ip so the
			// client's WaitStop can report a plausible stop.
			notif := make([]byte, 0, 24)
			notif = appendLE32(notif, attachedPID)
			notif = appendLE32(notif, 1) // tid
			ip := brkAddr
			if ip == 0 {
				ip = 0x4000
			}
			notif = appendLE64(notif, ip)
			writeFrame(append([]byte{0x40, 0x00, mid, 0x01}, notif...))
		case 6: // Detach -> ok
			reply(0x21, mid, nil)
		case 8: // Stop -> ok
			reply(0x21, mid, nil)
		default:
			reply(0x20, mid, []byte{0x16, 0, 0, 0}) // err EINVAL
		}
	}
}

func appendLE32(b []byte, v uint32) []byte {
	return append(b, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}

func appendLE64(b []byte, v uint64) []byte {
	return append(b, byte(v), byte(v>>8), byte(v>>16), byte(v>>24),
		byte(v>>32), byte(v>>40), byte(v>>48), byte(v>>56))
}

// ListenDSMSG serves mock pdebug connections on ln.
func ListenDSMSG(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go ServeDSMSG(c)
	}
}
