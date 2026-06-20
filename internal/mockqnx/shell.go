package mockqnx

import (
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

const mockInfo = "ARCHITECTURE=x86_64 CPU=x86_64 ENDIAN=le HASVERSION=1 HOSTNAME=qnxmock " +
	"MACHINE=x86pc NUM_SRVCS=1 OS=nto RELEASE=8.0 SYSNAME=QNX " +
	"TIMEZONE=UTC0 VERSION=2024/01/01-00:00:00UTC"

const pidinThreads = `     pid tid name               prio STATE       Blocked
       1   1 /procnto-smp-instr  255r RUNNING
       1   2 /procnto-smp-instr   10r RECEIVE     1
    4099   1 /proc/boot/devc-con   10r RECEIVE     1
    8200   1 /proc/boot/qconn      10r REPLY       1
    8200   2 /proc/boot/qconn      10r RECEIVE     1
    8300   1 /bin/sh               10r REPLY       1
`

const pidinMem = `     pid tid name               prio STATE             code  data        stack
       1   1 procnto-smp-instr   255r RUNNING            644K  6321K    4096(516K)
    8200   1 qconn                10r REPLY              52K   148K     4096(132K)
`

const pidinInfo = `CPU:X86_64 Release:8.0
Processes: 6 Threads: 8
FreeMemory: 805306368 TotalMemory: 1073741824
BootTime: 2024/01/01-00:00:00
`

const pidinPmem = `     pid tid name               prio STATE
    8200   1 qconn                10r REPLY
                0x08048000-0x08050000   r-x  /proc/boot/qconn
                0x08050000-0x08052000   rw-  /proc/boot/qconn
                0xb8300000-0xb8320000   rw-  [stack]
`

var pdebugRe = regexp.MustCompile(`pdebug\s+(\d+)`)
var pidinPRe = regexp.MustCompile(`pidin\s+-p\s+(\d+)`)

var rspOnce sync.Map // port -> started

// runShell simulates "/bin/sh -c <cmd>".
func runShell(cmd string) string {
	cmd = strings.TrimSpace(cmd)

	if m := pdebugRe.FindStringSubmatch(cmd); m != nil {
		port, _ := strconv.Atoi(m[1])
		startMockPdebug(port)
		return "4242\r\n" // the Run() wrapper appends "& echo $!"
	}

	switch {
	case strings.Contains(cmd, "pidin") && strings.Contains(cmd, "mem") && pidinPRe.MatchString(cmd):
		return pidinPmem
	case strings.Contains(cmd, "pidin") && strings.Contains(cmd, "mem"):
		return pidinMem
	case strings.Contains(cmd, "pidin") && strings.Contains(cmd, "info"):
		return pidinInfo
	case pidinPRe.MatchString(cmd):
		m := pidinPRe.FindStringSubmatch(cmd)
		return filterPidin(pidinThreads, m[1])
	case strings.Contains(cmd, "pidin"):
		return pidinThreads
	case strings.HasPrefix(cmd, "ls "):
		return "total 0\r\ndrwxr-xr-x  2 root root 0 Jan  1 00:00 .\r\n-rwxr-xr-x  1 root root 52K Jan  1 00:00 qconn\r\n"
	case strings.HasPrefix(cmd, "uname"):
		return "QNX qnxmock 8.0 2024/01/01-00:00:00UTC x86pc x86_64\r\n"
	case strings.HasPrefix(cmd, "echo "):
		return strings.TrimPrefix(cmd, "echo ") + "\r\n"
	default:
		return "mock-shell: " + cmd + "\r\n"
	}
}

func filterPidin(listing, pid string) string {
	var b strings.Builder
	for _, ln := range strings.Split(listing, "\n") {
		f := strings.Fields(ln)
		if len(f) == 0 {
			continue
		}
		if f[0] == "pid" || f[0] == pid {
			b.WriteString(ln + "\n")
		}
	}
	return b.String()
}

func startMockPdebug(port int) {
	if _, loaded := rspOnce.LoadOrStore(port, true); loaded {
		return
	}
	go func() {
		if err := serveMockRSP(port); err != nil {
			slog.Default().Debug("mock pdebug stopped", "port", port, "err", err)
			rspOnce.Delete(port)
		}
	}()
}
