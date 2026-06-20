// Command qconn-proxy is a transparent, logging TCP proxy for the qconn
// protocol. Point qconn-mcp at the proxy's listen address and the proxy at the
// qconn daemon to get annotated bidirectional traces for debugging.
//
//	qconn-proxy -listen :8000 -target 192.168.1.50:8000 -log-format json
package main

import (
	"os"

	"github.com/johnson-thomas/qnx8-qconn-mcp/internal/obs"
	"github.com/johnson-thomas/qnx8-qconn-mcp/internal/proxy"

	"flag"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:8000", "address to listen on")
	target := flag.String("target", "", "real qconn target host:port (required)")
	logFormat := flag.String("log-format", "text", "text|json")
	logLevel := flag.String("log-level", "info", "debug|info|warn|error")
	flag.Parse()

	if *target == "" {
		flag.Usage()
		os.Exit(2)
	}

	log := obs.Setup(obs.Options{Level: *logLevel, Format: *logFormat})
	p := &proxy.Proxy{Listen: *listen, Target: *target, Logger: log}
	if err := p.Run(); err != nil {
		log.Error("proxy exited", "err", err)
		os.Exit(1)
	}
}
