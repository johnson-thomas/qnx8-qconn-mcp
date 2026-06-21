// Package mcpserver exposes the qconn capabilities as MCP tools over the
// Streamable HTTP transport using the official Go MCP SDK
// (github.com/modelcontextprotocol/go-sdk).
package mcpserver

import (
	"context"
	"crypto/subtle"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/johnson-thomas/qnx8-qconn-mcp/internal/debug"
	"github.com/johnson-thomas/qnx8-qconn-mcp/internal/qconn"
)

// Config configures the MCP server and its connection to the qconn target.
type Config struct {
	// Target qconn daemon.
	QconnHost string
	QconnPort int
	Timeout   time.Duration

	// MCP HTTP transport.
	BindAddr string // e.g. 127.0.0.1:8077
	Path     string // mount path, default /mcp
	Token    string // optional bearer token required on requests

	// Debug bridge.
	DebugPort int    // TCP port pdebug is told to listen on (default 8001)
	PdebugCmd string // pdebug launch template, default "pdebug %d"

	// Serial pdebug transport (optional). When DebugSerial is set, debug tools
	// reach pdebug over this serial device at DebugBaud instead of over TCP.
	DebugSerial string
	DebugBaud   int

	Logger *slog.Logger
}

// Server bundles the MCP server, the lazily-established qconn client, and the
// debug manager.
type Server struct {
	cfg Config
	log *slog.Logger

	mu   sync.Mutex
	conn *qconn.Client // lazily dialed, reused across tool calls
	mgr  *debug.Manager

	dbgMu    sync.Mutex
	sessions map[string]*qnxSession // active pdebug (DSMSG) sessions by id

	mcp *mcp.Server
}

// New builds the MCP server and registers all tools.
func New(cfg Config) *Server {
	if cfg.Path == "" {
		cfg.Path = "/mcp"
	}
	if cfg.QconnPort == 0 {
		cfg.QconnPort = qconn.DefaultPort
	}
	if cfg.DebugPort == 0 {
		cfg.DebugPort = 8001
	}
	if cfg.DebugBaud == 0 {
		cfg.DebugBaud = 115200
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	s := &Server{
		cfg:      cfg,
		log:      cfg.Logger.With("component", "mcp"),
		sessions: map[string]*qnxSession{},
	}
	s.mcp = mcp.NewServer(&mcp.Implementation{
		Name:    "qnx-qconn",
		Title:   "QNX qconn MCP server",
		Version: "0.1.0",
	}, nil)
	s.registerTools()
	return s
}

// client returns a connected, reused qconn client, dialing on first use.
func (s *Server) client(ctx context.Context) (*qconn.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn != nil {
		return s.conn, nil
	}
	cli, err := qconn.Dial(ctx, qconn.Config{
		Host:    s.cfg.QconnHost,
		Port:    s.cfg.QconnPort,
		Timeout: s.cfg.Timeout,
		Logger:  s.cfg.Logger,
	})
	if err != nil {
		return nil, err
	}
	s.conn = cli
	pcmd := s.cfg.PdebugCmd
	if pcmd == "" {
		pcmd = "on -d pdebug %d </dev/null >/dev/null 2>&1"
	}
	s.mgr = debug.NewManager(cli, s.cfg.QconnHost, s.cfg.Logger)
	s.mgr.PdebugCmd = pcmd
	return cli, nil
}

// resetClient drops a broken connection so the next call redials.
func (s *Server) resetClient() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn != nil {
		_ = s.conn.Close()
		s.conn = nil
		s.mgr = nil
	}
}

// Handler returns the Streamable HTTP handler, wrapped with optional bearer
// auth and request logging.
func (s *Server) Handler() http.Handler {
	streamable := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return s.mcp },
		&mcp.StreamableHTTPOptions{},
	)
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle(s.cfg.Path, s.withAuth(streamable))
	mux.Handle(s.cfg.Path+"/", s.withAuth(streamable))
	return s.withLogging(mux)
}

func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.Token != "" {
			got := r.Header.Get("Authorization")
			want := "Bearer " + s.cfg.Token
			if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
				s.log.Warn("unauthorized request", "remote", r.RemoteAddr)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		s.log.Debug("http request", "method", r.Method, "path", r.URL.Path, "remote", r.RemoteAddr)
		next.ServeHTTP(w, r)
		s.log.Debug("http done", "path", r.URL.Path, "dur", time.Since(start).String())
	})
}

// Close releases the qconn connection and any debug sessions.
func (s *Server) Close() error {
	s.dbgMu.Lock()
	for id, sess := range s.sessions {
		_ = sess.Close()
		delete(s.sessions, id)
	}
	s.dbgMu.Unlock()
	s.resetClient()
	return nil
}

// fail is a small helper to turn an error into a tool error result.
func fail(format string, a ...any) (*mcp.CallToolResult, error) {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(format, a...)}},
	}, nil
}
