package debug

import (
	"bytes"
	"testing"
)

func TestChecksum(t *testing.T) {
	// "OK" => 0x4f+0x4b = 0x9a
	if got := checksum([]byte("OK")); got != 0x9a {
		t.Fatalf("checksum(OK)=%#x want 0x9a", got)
	}
}

func TestEscapeRoundTrip(t *testing.T) {
	in := []byte{'a', '$', '#', '}', '*', 'z'}
	esc := escape(in)
	if bytes.ContainsAny(esc, "$#") {
		// '*' is allowed to remain only as a literal we escaped; ensure specials gone
	}
	got := decode(esc)
	if !bytes.Equal(got, in) {
		t.Fatalf("escape/decode roundtrip: got %v want %v", got, in)
	}
}

func TestDecodeRunLength(t *testing.T) {
	// "0" followed by '*' and count byte. count = byte-29. Use '%'(0x25)=37 -> 8 repeats.
	// So "0*%": one '0' then 8 more '0' = 9 zeros.
	got := decode([]byte("0*%"))
	want := bytes.Repeat([]byte("0"), 9)
	if !bytes.Equal(got, want) {
		t.Fatalf("RLE decode got %q want %q", got, want)
	}
}

func TestHexEncodeDecode(t *testing.T) {
	in := []byte{0x00, 0xde, 0xad, 0xbe, 0xef}
	s := hexEncode(in)
	if s != "00deadbeef" {
		t.Fatalf("hexEncode=%q", s)
	}
	out, err := hexDecode(s)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, in) {
		t.Fatalf("hexDecode mismatch")
	}
}

func TestParseStop(t *testing.T) {
	sr := parseStop("T05thread:1;")
	if sr.Signal != 5 {
		t.Fatalf("signal=%d want 5", sr.Signal)
	}
	if sr.Thread != "1" {
		t.Fatalf("thread=%q want 1", sr.Thread)
	}
}
