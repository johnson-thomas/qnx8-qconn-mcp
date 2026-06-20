package qnxdbg

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// baudRates maps common bit rates to their termios speed constants.
var baudRates = map[int]uint32{
	9600:    unix.B9600,
	19200:   unix.B19200,
	38400:   unix.B38400,
	57600:   unix.B57600,
	115200:  unix.B115200,
	230400:  unix.B230400,
	460800:  unix.B460800,
	921600:  unix.B921600,
	1000000: unix.B1000000,
}

// ConnectSerial opens a raw serial line to a pdebug agent running over a serial
// transport (target side e.g. `pdebug /dev/ser1` or `devc-ser... pdebug`) and
// performs the DSMSG connect handshake. The same client commands then work
// exactly as over TCP.
//
// NOTE: this needs a serial line DEDICATED to pdebug. If the only UART is the
// system console (as on the RPi400 here, /dev/ttyUSB0), pdebug and the console
// cannot share it — use the network transport in that case, and keep the serial
// line for console recovery (see support_tools/bpi_console.py).
func ConnectSerial(ctx context.Context, device string, baud int, log *slog.Logger, timeout time.Duration) (*Client, error) {
	f, err := openSerial(device, baud)
	if err != nil {
		return nil, fmt.Errorf("qnxdbg open serial %s: %w", device, err)
	}
	label := fmt.Sprintf("%s@%d", device, baud)
	c, err := newClient(f, label, log, timeout)
	if err != nil {
		f.Close()
		return nil, err
	}
	return c, nil
}

// openSerial opens device in raw mode at baud and returns the *os.File. The file
// is pollable, so the client's SetDeadline-based timeouts apply.
func openSerial(device string, baud int) (*os.File, error) {
	speed, ok := baudRates[baud]
	if !ok {
		return nil, fmt.Errorf("unsupported baud rate %d", baud)
	}
	// O_NONBLOCK registers the fd with the runtime poller so SetReadDeadline
	// works; O_NOCTTY keeps the tty from becoming our controlling terminal.
	f, err := os.OpenFile(device, unix.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	fd := int(f.Fd())
	t := &unix.Termios{
		Cflag:  unix.CREAD | unix.CLOCAL | unix.CS8,
		Ispeed: speed,
		Ospeed: speed,
	}
	// Raw mode. VMIN=1/VTIME=0: a read wants at least one byte. Because the fd
	// stays O_NONBLOCK, an empty read returns EAGAIN (not a 0-length read, which
	// Go would surface as io.EOF and spin the frame reader) and the runtime
	// poller blocks until data arrives or the client's read deadline fires.
	t.Cc[unix.VMIN] = 1
	t.Cc[unix.VTIME] = 0
	if err := unix.IoctlSetTermios(fd, unix.TCSETS, t); err != nil {
		f.Close()
		return nil, fmt.Errorf("configure termios: %w", err)
	}
	return f, nil
}
