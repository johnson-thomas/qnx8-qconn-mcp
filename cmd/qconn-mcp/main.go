// Command qconn-mcp is an MCP server (Streamable HTTP transport) that bridges
// to a QNX qconn daemon, exposing process/memory/system introspection, process
// control, file transfer, program launch and pdebug/GDB-RSP debugging as MCP
// tools. See README.md for the full protocol and capability reference.
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/johnson-thomas/qnx8-qconn-mcp/internal/config"
	"github.com/johnson-thomas/qnx8-qconn-mcp/internal/mcpserver"
	"github.com/johnson-thomas/qnx8-qconn-mcp/internal/obs"
)

func main() {
	cfg, err := config.Load(os.Args[1:])
	if err != nil {
		// flag.ContinueOnError already printed usage/errors.
		os.Exit(2)
	}

	log := obs.Setup(obs.Options{Level: cfg.LogLevel, Format: cfg.LogFormat, Trace: cfg.Trace})
	log.Info("starting qconn-mcp",
		"target", cfg.QconnHost, "qconn_port", cfg.QconnPort,
		"bind", cfg.BindAddr, "path", cfg.Path, "trace", cfg.Trace)

	srv := mcpserver.New(mcpserver.Config{
		QconnHost: cfg.QconnHost,
		QconnPort: cfg.QconnPort,
		Timeout:   cfg.Timeout,
		BindAddr:  cfg.BindAddr,
		Path:      cfg.Path,
		Token:     cfg.Token,
		DebugPort:   cfg.DebugPort,
		PdebugCmd:   cfg.PdebugCmd,
		DebugSerial: cfg.DebugSerial,
		DebugBaud:   cfg.DebugBaud,
		Logger:      log,
	})
	defer srv.Close()

	httpSrv := &http.Server{
		Addr:    cfg.BindAddr,
		Handler: srv.Handler(),
	}

	go func() {
		log.Info("MCP Streamable HTTP listening", "url", "http://"+cfg.BindAddr+cfg.Path)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server failed", "err", err)
			os.Exit(1)
		}
	}()

	// Graceful shutdown on SIGINT/SIGTERM.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	log.Info("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(ctx)
}
