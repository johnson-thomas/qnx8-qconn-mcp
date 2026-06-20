package qnxdbg

import (
	"context"
	"net"
	"testing"
	"time"
)

// transport is satisfied by *net.Conn (TCP) and *os.File (serial). This guards
// the abstraction the serial transport relies on.
func TestNetConnIsTransport(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()
	var _ transport = c1 // compile-time assertion
}

func TestOpenSerialRejectsBadBaud(t *testing.T) {
	if _, err := openSerial("/dev/null", 12345); err == nil {
		t.Fatal("expected error for unsupported baud rate")
	}
}

func TestConnectSerialMissingDevice(t *testing.T) {
	_, err := ConnectSerial(context.Background(), "/dev/does-not-exist-qnxdbg", 115200, nil, time.Second)
	if err == nil {
		t.Fatal("expected error opening nonexistent serial device")
	}
}
