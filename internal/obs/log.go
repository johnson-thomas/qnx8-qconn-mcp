// Package obs provides structured logging and wire-tracing helpers shared by
// the qconn client, the MCP server, the mock and the proxy.
package obs

import (
	"context"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"strings"
)

// Options configure the process-wide logger.
type Options struct {
	Level  string // debug|info|warn|error
	Format string // text|json
	Trace  bool   // enable wire-level hex/ASCII dumps (very verbose)
	Out    io.Writer
}

// traceEnabled is a package-level flag toggled by Setup so that hot-path wire
// dump calls can be cheaply skipped when tracing is off.
var traceEnabled bool

// Setup builds an slog.Logger from Options and installs it as the default.
// It returns the logger for callers that prefer explicit handles.
func Setup(o Options) *slog.Logger {
	if o.Out == nil {
		o.Out = os.Stderr
	}
	traceEnabled = o.Trace

	lvl := new(slog.LevelVar)
	switch strings.ToLower(o.Level) {
	case "debug":
		lvl.Set(slog.LevelDebug)
	case "warn", "warning":
		lvl.Set(slog.LevelWarn)
	case "error":
		lvl.Set(slog.LevelError)
	default:
		lvl.Set(slog.LevelInfo)
	}

	hopts := &slog.HandlerOptions{Level: lvl, ReplaceAttr: redact}
	var h slog.Handler
	if strings.ToLower(o.Format) == "json" {
		h = slog.NewJSONHandler(o.Out, hopts)
	} else {
		h = slog.NewTextHandler(o.Out, hopts)
	}
	l := slog.New(h)
	slog.SetDefault(l)
	return l
}

// TraceEnabled reports whether wire tracing is active.
func TraceEnabled() bool { return traceEnabled }

// redact scrubs sensitive values (e.g. bearer tokens) from log records.
func redact(_ []string, a slog.Attr) slog.Attr {
	switch strings.ToLower(a.Key) {
	case "token", "authorization", "auth", "secret", "password":
		a.Value = slog.StringValue("[redacted]")
	}
	return a
}

// Trace logs a wire-level event with a side-channel direction marker and a
// hex/ASCII dump of the bytes. It is a no-op when tracing is disabled.
//
// dir is conventionally ">>" (host -> target) or "<<" (target -> host).
func Trace(l *slog.Logger, where, dir string, b []byte) {
	if !traceEnabled || len(b) == 0 {
		return
	}
	if l == nil {
		l = slog.Default()
	}
	l.LogAttrs(context.Background(), slog.LevelDebug, "wire",
		slog.String("where", where),
		slog.String("dir", dir),
		slog.Int("len", len(b)),
		slog.String("ascii", printable(b)),
		slog.String("hex", hex.EncodeToString(b)),
	)
}

// printable renders bytes as ASCII, replacing non-printable bytes with '.'.
func printable(b []byte) string {
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
