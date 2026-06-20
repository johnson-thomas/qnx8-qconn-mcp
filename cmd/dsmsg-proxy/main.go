// Command dsmsg-proxy is a transparent TCP proxy that decodes the QNX pdebug
// DSMSG protocol in both directions. It is a development aid for inspecting and
// debugging DSMSG traffic between a debug client and a pdebug agent.
//
// Topology:
//
//	debug client  ->  dsmsg-proxy (this host, decodes + logs)  ->  pdebug on the target
//
// On the QNX target run e.g.:  on -d pdebug 8001
// Then:                        dsmsg-proxy -listen :8001 -target 192.168.2.10:8001
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"sync"
	"time"

	"github.com/johnson-thomas/qnx8-qconn-mcp/internal/qnxdbg"
)

func main() {
	listen := flag.String("listen", "0.0.0.0:8001", "address to listen on (gdb connects here)")
	target := flag.String("target", "192.168.2.10:8001", "QNX pdebug host:port to forward to")
	logfile := flag.String("log", "", "also append decoded log to this file")
	flag.Parse()

	out := io.Writer(os.Stdout)
	if *logfile != "" {
		f, err := os.OpenFile(*logfile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			log.Fatalf("open log: %v", err)
		}
		defer f.Close()
		out = io.MultiWriter(os.Stdout, f)
	}

	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatalf("listen %s: %v", *listen, err)
	}
	fmt.Fprintf(out, "dsmsg-proxy listening on %s -> %s\n", *listen, *target)
	fmt.Fprintf(out, "point gdb at this host's IP:%s (target qnx <ip>:%s)\n",
		portOf(*listen), portOf(*listen))
	for {
		c, err := ln.Accept()
		if err != nil {
			log.Printf("accept: %v", err)
			continue
		}
		go handle(c, *target, out)
	}
}

func portOf(addr string) string {
	if _, p, err := net.SplitHostPort(addr); err == nil {
		return p
	}
	return addr
}

func handle(client net.Conn, target string, out io.Writer) {
	id := client.RemoteAddr().String()
	logf(out, "==== session open: gdb=%s ====", id)
	up, err := net.Dial("tcp", target)
	if err != nil {
		logf(out, "dial target %s: %v", target, err)
		client.Close()
		return
	}
	var wg sync.WaitGroup
	wg.Add(2)
	// "->" is gdb->pdebug (commands); "<-" is pdebug->gdb (responses/notifies).
	go func() { defer wg.Done(); pump(client, up, "gdb->pdebug", out) }()
	go func() { defer wg.Done(); pump(up, client, "pdebug->gdb", out) }()
	wg.Wait()
	client.Close()
	up.Close()
	logf(out, "==== session closed: gdb=%s ====", id)
}

func pump(src, dst net.Conn, dir string, out io.Writer) {
	var sc qnxdbg.FrameScanner
	buf := make([]byte, 32*1024)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			_, _ = dst.Write(chunk) // forward verbatim
			payloads, raws := sc.Feed(chunk)
			for i, p := range payloads {
				logf(out, "[%s] %s | raw=%s", dir, qnxdbg.Annotate(p), qnxdbg.HexDump(raws[i]))
			}
		}
		if err != nil {
			_ = dst.Close()
			return
		}
	}
}

func logf(out io.Writer, format string, a ...any) {
	fmt.Fprintf(out, "%s "+format+"\n", append([]any{time.Now().Format("15:04:05.000")}, a...)...)
}
