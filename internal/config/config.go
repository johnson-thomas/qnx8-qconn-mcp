// Package config loads MCP/qconn server configuration from (in increasing
// precedence) a YAML file, environment variables, and command-line flags.
package config

import (
	"flag"
	"os"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

// Config holds all runtime configuration.
type Config struct {
	QconnHost string        `yaml:"qconn_host"`
	QconnPort int           `yaml:"qconn_port"`
	Timeout   time.Duration `yaml:"timeout"`

	BindAddr string `yaml:"bind_addr"`
	Path     string `yaml:"path"`
	Token    string `yaml:"token"`

	DebugPort int    `yaml:"debug_port"`
	PdebugCmd string `yaml:"pdebug_cmd"`

	// Serial pdebug transport (alternative to TCP). When DebugSerial is set, the
	// debug tools reach pdebug over this serial device at DebugBaud instead of
	// dialing DebugPort. Requires a serial line dedicated to pdebug.
	DebugSerial string `yaml:"debug_serial"`
	DebugBaud   int    `yaml:"debug_baud"`

	LogLevel  string `yaml:"log_level"`
	LogFormat string `yaml:"log_format"`
	Trace     bool   `yaml:"trace"`
}

// Default returns the baseline configuration.
func Default() Config {
	return Config{
		QconnHost: "127.0.0.1",
		QconnPort: 8000,
		Timeout:   30 * time.Second,
		BindAddr:  "127.0.0.1:8077",
		Path:      "/mcp",
		DebugPort: 8001,
		PdebugCmd: "on -d pdebug %d </dev/null >/dev/null 2>&1",
		DebugBaud: 115200,
		LogLevel:  "info",
		LogFormat: "text",
	}
}

// Load parses flags (and reads -config/QCONN_CONFIG YAML + env overrides).
func Load(args []string) (Config, error) {
	cfg := Default()

	fs := flag.NewFlagSet("qconn-mcp", flag.ContinueOnError)
	configPath := fs.String("config", os.Getenv("QCONN_CONFIG"), "path to YAML config file")

	// Pre-parse to discover -config, then layer env, then flags on top.
	_ = fs.Parse(args)
	if *configPath != "" {
		if err := loadYAML(*configPath, &cfg); err != nil {
			return cfg, err
		}
	}
	applyEnv(&cfg)

	// Second pass: bind flags with current values as defaults so explicit flags win.
	fs2 := flag.NewFlagSet("qconn-mcp", flag.ContinueOnError)
	fs2.String("config", *configPath, "path to YAML config file")
	fs2.StringVar(&cfg.QconnHost, "qconn-host", cfg.QconnHost, "qconn target host/IP")
	fs2.IntVar(&cfg.QconnPort, "qconn-port", cfg.QconnPort, "qconn target TCP port")
	fs2.DurationVar(&cfg.Timeout, "timeout", cfg.Timeout, "per-operation I/O timeout")
	fs2.StringVar(&cfg.BindAddr, "bind", cfg.BindAddr, "MCP HTTP bind address")
	fs2.StringVar(&cfg.Path, "path", cfg.Path, "MCP HTTP mount path")
	fs2.StringVar(&cfg.Token, "token", cfg.Token, "optional bearer token required on requests")
	fs2.IntVar(&cfg.DebugPort, "debug-port", cfg.DebugPort, "TCP port pdebug listens on")
	fs2.StringVar(&cfg.PdebugCmd, "pdebug-cmd", cfg.PdebugCmd, "pdebug launch template (printf %d port)")
	fs2.StringVar(&cfg.DebugSerial, "debug-serial", cfg.DebugSerial, "serial device for pdebug transport (e.g. /dev/ttyUSB1); empty = use TCP")
	fs2.IntVar(&cfg.DebugBaud, "debug-baud", cfg.DebugBaud, "baud rate for -debug-serial")
	fs2.StringVar(&cfg.LogLevel, "log-level", cfg.LogLevel, "debug|info|warn|error")
	fs2.StringVar(&cfg.LogFormat, "log-format", cfg.LogFormat, "text|json")
	fs2.BoolVar(&cfg.Trace, "trace", cfg.Trace, "enable wire-level hex/ASCII trace logging")
	if err := fs2.Parse(args); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func loadYAML(path string, cfg *Config) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(b, cfg)
}

func applyEnv(cfg *Config) {
	if v := os.Getenv("QCONN_HOST"); v != "" {
		cfg.QconnHost = v
	}
	if v := os.Getenv("QCONN_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.QconnPort = n
		}
	}
	if v := os.Getenv("MCP_ADDR"); v != "" {
		cfg.BindAddr = v
	}
	if v := os.Getenv("MCP_TOKEN"); v != "" {
		cfg.Token = v
	}
	if v := os.Getenv("QCONN_DEBUG_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.DebugPort = n
		}
	}
	if v := os.Getenv("LOG_LEVEL"); v != "" {
		cfg.LogLevel = v
	}
	if os.Getenv("QCONN_TRACE") == "1" {
		cfg.Trace = true
	}
}
