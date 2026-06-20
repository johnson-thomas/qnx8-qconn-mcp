package qnxdbg

import "bytes"

import "testing"

func TestChecksum(t *testing.T) {
	// Reset frame ~00ff~: checksum of [0x00] is 0xff.
	if got := checksum([]byte{0x00}); got != 0xff {
		t.Fatalf("checksum([0])=%#x want 0xff", got)
	}
	// TargetConnect body 00 00 00 01 00 07 00 00 -> 0xf7 (from rpdbg.py).
	if got := checksum([]byte{0, 0, 0, 1, 0, 7, 0, 0}); got != 0xf7 {
		t.Fatalf("connect checksum=%#x want 0xf7", got)
	}
}

func TestEscapeRoundTrip(t *testing.T) {
	in := []byte{0x00, 0x7e, 0x7d, 0x41, 0x7e, 0xff}
	esc := escape(in)
	if bytes.IndexByte(esc, frameChar) >= 0 {
		t.Fatalf("escaped data still contains 0x7e: %x", esc)
	}
	got := unescape(esc)
	if !bytes.Equal(got, in) {
		t.Fatalf("escape/unescape roundtrip: got %x want %x", got, in)
	}
}

func TestFrameMatchesReference(t *testing.T) {
	// TargetConnect frame from rpdbg.py: 7e0000000100070000f77e
	payload := []byte{0x00, 0x00, 0x00, 0x01, 0x00, 0x07, 0x00, 0x00}
	got := frame(payload)
	want := []byte{0x7e, 0x00, 0x00, 0x00, 0x01, 0x00, 0x07, 0x00, 0x00, 0xf7, 0x7e}
	if !bytes.Equal(got, want) {
		t.Fatalf("frame = %x want %x", got, want)
	}
}

// TestBreakpointFrameMatchesRealPdebug pins the DSMSG Brk encoding to the QNX 8
// pdebug wire format (validated on a RaspberryPi400):
//
//	set   compute @0x34e93dd8b0, mid=0xcd: 7e0e01cd0100000000b0d83de934000000407e
//	clear same addr,            mid=0xdc: 7e0e01dc01ffffffffb0d83de934000000357e
//
// The Brk subcmd byte is the breakpoint type (_DEBUG_BREAK_EXEC=1); the body is
// size:int32 (0=set, -1=remove) followed by addr:uint64.
func TestBreakpointFrameMatchesRealPdebug(t *testing.T) {
	const addr = uint64(0x34e93dd8b0)
	brkFrame := func(mid byte, size int32) []byte {
		body := append(le32(uint32(size)), le64(addr)...)
		payload := append([]byte{cmdBrk, brkTypeExec, mid, chanDebug}, body...)
		return frame(payload)
	}
	set := brkFrame(0xcd, 0)
	wantSet := []byte{0x7e, 0x0e, 0x01, 0xcd, 0x01, 0x00, 0x00, 0x00, 0x00,
		0xb0, 0xd8, 0x3d, 0xe9, 0x34, 0x00, 0x00, 0x00, 0x40, 0x7e}
	if !bytes.Equal(set, wantSet) {
		t.Fatalf("set brk frame = %x want %x", set, wantSet)
	}
	clr := brkFrame(0xdc, -1)
	wantClr := []byte{0x7e, 0x0e, 0x01, 0xdc, 0x01, 0xff, 0xff, 0xff, 0xff,
		0xb0, 0xd8, 0x3d, 0xe9, 0x34, 0x00, 0x00, 0x00, 0x35, 0x7e}
	if !bytes.Equal(clr, wantClr) {
		t.Fatalf("clear brk frame = %x want %x", clr, wantClr)
	}
}

// TestRunFrameMatchesRealPdebug pins the DSMSG Run (continue/step) encoding to
// the QNX 8 pdebug wire format:
//
//	continue, mid=212: 7e0d00d40100000000010000000000000000000000000000001c7e
//	stepi,    mid=235: 7e0d01eb010000000001000000000000000000000000000000047e
//
// Both send body flags=0, tid=1 + 12 reserved bytes; single-step is selected by
// subcmd=1 (continue is subcmd=0), NOT by a debug_run flag.
func TestRunFrameMatchesRealPdebug(t *testing.T) {
	runFrame := func(mid, subcmd byte) []byte {
		body := append(append(le32(0), le32(1)...), make([]byte, 12)...)
		return frame(append([]byte{cmdRun, subcmd, mid, chanDebug}, body...))
	}
	cont := runFrame(212, runContinue)
	wantCont := []byte{0x7e, 0x0d, 0x00, 0xd4, 0x01, 0x00, 0x00, 0x00, 0x00,
		0x01, 0x00, 0x00, 0x00, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x1c, 0x7e}
	if !bytes.Equal(cont, wantCont) {
		t.Fatalf("continue run frame = %x want %x", cont, wantCont)
	}
	step := runFrame(235, runStep)
	wantStep := []byte{0x7e, 0x0d, 0x01, 0xeb, 0x01, 0x00, 0x00, 0x00, 0x00,
		0x01, 0x00, 0x00, 0x00, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x04, 0x7e}
	if !bytes.Equal(step, wantStep) {
		t.Fatalf("step run frame = %x want %x", step, wantStep)
	}
}
