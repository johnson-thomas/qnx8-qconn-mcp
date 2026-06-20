// Package proxy implements a transparent TCP proxy that logs every byte in both
// directions with hex/ASCII dumps and lightweight qconn protocol annotation. It
// is a development aid for inspecting and debugging qconn-mcp's own traffic to a
// qconn daemon.
package proxy

import (
	"fmt"
	"io"
	"log/slog"
	"net"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Proxy forwards TCP from Listen to Target, tracing traffic.
type Proxy struct {
	Listen string
	Target string
	Logger *slog.Logger
}

var annotRe = regexp.MustCompile(`service\s+(\w+)|<qconn-(\w+)>|start/flags|^o:|kill\s|versions`)

// Run accepts connections until the listener is closed.
func (p *Proxy) Run() error {
	ln, err := net.Listen("tcp", p.Listen)
	if err != nil {
		return err
	}
	p.Logger.Info("proxy listening", "listen", p.Listen, "target", p.Target)
	for {
		c, err := ln.Accept()
		if err != nil {
			return err
		}
		go p.handle(c)
	}
}

func (p *Proxy) handle(client net.Conn) {
	defer client.Close()
	id := client.RemoteAddr().String()
	upstream, err := net.Dial("tcp", p.Target)
	if err != nil {
		p.Logger.Error("proxy dial target failed", "err", err, "client", id)
		return
	}
	defer upstream.Close()
	p.Logger.Info("proxy session open", "client", id, "target", p.Target)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); p.pump(client, upstream, ">>", id) }() // client -> target
	go func() { defer wg.Done(); p.pump(upstream, client, "<<", id) }() // target -> client
	wg.Wait()
	p.Logger.Info("proxy session closed", "client", id)
}

// pump copies src->dst, logging each chunk.
func (p *Proxy) pump(src, dst net.Conn, dir, id string) {
	buf := make([]byte, 16*1024)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			_, _ = dst.Write(chunk)
			p.log(dir, id, chunk)
		}
		if err != nil {
			if err != io.EOF {
				p.Logger.Debug("proxy pump end", "dir", dir, "client", id, "err", err)
			}
			// Close the peer write side so the other direction also unblocks.
			_ = dst.Close()
			return
		}
	}
}

func (p *Proxy) log(dir, id string, b []byte) {
	annot := annotate(b)
	p.Logger.Info("proxy",
		slog.String("dir", dir),
		slog.String("client", id),
		slog.Int("len", len(b)),
		slog.String("annot", annot),
		slog.String("ascii", asciiDump(b)),
		slog.String("hex", hexDump(b)),
		slog.String("ts", time.Now().Format(time.RFC3339Nano)),
	)
}

func annotate(b []byte) string {
	s := string(b)
	var hits []string
	for _, m := range annotRe.FindAllString(s, -1) {
		hits = append(hits, strings.TrimSpace(m))
	}
	return strings.Join(hits, ",")
}

func asciiDump(b []byte) string {
	var sb strings.Builder
	for _, c := range b {
		if c >= 0x20 && c < 0x7f {
			sb.WriteByte(c)
		} else {
			sb.WriteByte('.')
		}
	}
	return sb.String()
}

func hexDump(b []byte) string {
	const max = 512
	trunc := b
	if len(trunc) > max {
		trunc = trunc[:max]
	}
	var sb strings.Builder
	for i, c := range trunc {
		if i > 0 {
			sb.WriteByte(' ')
		}
		fmt.Fprintf(&sb, "%02x", c)
	}
	if len(b) > max {
		sb.WriteString(" ...")
	}
	return sb.String()
}
