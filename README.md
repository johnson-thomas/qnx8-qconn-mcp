# qnx8-qconn-mcp — an MCP server for the QNX `qconn` target agent

`qconn-mcp` is a [Model Context Protocol](https://modelcontextprotocol.io) (MCP)
server, written in Go, that connects to the **`qconn`** daemon on a **QNX
Neutrino** target (QNX 6.x – **8.0**) and exposes its capabilities as MCP tools
over the **Streamable HTTP** transport: system / process / thread / memory
introspection, process control, file transfer, program launch, and
**source/machine-level debugging** through QNX's `pdebug` agent.

It is **mock-first**: a protocol-faithful `mock-qconn` lets you develop and test
without QNX hardware.

> ⚠️ **Security:** `qconn` performs **no authentication** and runs every request
> **as root**. Never expose `qconn` (or this bridge) to an untrusted network.
> See [Security](#security).

---

## Table of contents

- [What is qconn?](#what-is-qconn)
- [Capabilities](#capabilities)
- [MCP tool catalog](#mcp-tool-catalog)
- [Build & run](#build--run)
- [Configuration](#configuration)
- [Testing](#testing)
- [Architecture](#architecture)
- [Security](#security)
- [Limitations & roadmap](#limitations--roadmap)
- [License & provenance](#license--provenance)
- [Sources & references](#sources--references)

---

## What is qconn?

`qconn` is the QNX **target agent**: a daemon (default **TCP port 8000**) used to
inspect and control a running QNX system — listing processes and threads, reading
memory and system statistics, transferring files, launching programs, and
debugging them. It is a microkernel-friendly façade over QNX primitives such as
the `/proc` filesystem, the `pidin` utility, and the `pdebug` debug agent.

Key facts:

- Default port **8000** (`qconn port=N` to change).
- Requires **root**, and **`pdebug`** in the path for debugging.
- Intended for **development only** — QNX's own docs warn it "lets anyone run any
  application on your target system as the superuser."

This project is an independent implementation built for **interoperability** —
see [License & provenance](#license--provenance).

---

## Capabilities

| Capability | Mechanism |
|---|---|
| Processes / threads / states | `pidin` (parsed + raw) |
| Per-process & system memory | `pidin mem` / `pidin info` |
| Raw memory read/write | the file service on `/proc/<pid>/as` |
| Resources (fds, signals, timers, channels, IRQs, env, sched) | `pidin <kind>` |
| File transfer / management | the `qconn` file service |
| Program launch & exec | the `qconn` launcher service |
| Signals | the `qconn` cntl service |
| **Debugging** | the QNX **DSMSG** protocol to `pdebug` (TCP or serial) |

Introspection tools return **structured fields plus the raw text**, so the
ground truth is always available across QNX releases.

---

## MCP tool catalog

35 tools, all returning structured JSON. Mounted at `POST /mcp`.

**System** — `qconn_system_info`, `qconn_list_services`, `qconn_system_memory`

**Processes** — `qconn_list_processes`, `qconn_process_info`,
`qconn_process_memory`, `qconn_resources`

**Memory** — `qconn_memory_map`, `qconn_read_memory`, `qconn_write_memory`

**Exec / launch** — `qconn_exec`, `qconn_run`

**Control** — `qconn_signal`, `qconn_kill`

**Files** — `qconn_read_file`, `qconn_write_file`, `qconn_stat`,
`qconn_list_dir`, `qconn_delete`, `qconn_mkdir`, `qconn_chmod`

**Debug (pdebug / QNX DSMSG)** — `qconn_debug_attach`, `qconn_debug_launch`
(start a program under the debugger, stopped at entry — debug from `main`),
`qconn_debug_continue`,
`qconn_debug_step`, `qconn_debug_break_set`, `qconn_debug_break_clear`,
`qconn_debug_read_registers` (decoded aarch64), `qconn_debug_write_registers`,
`qconn_debug_read_memory`, `qconn_debug_write_memory`, `qconn_debug_select_thread`,
`qconn_debug_threads`, `qconn_debug_target_info`, `qconn_debug_detach`.

Attach to a running process: `qconn_list_processes` → `qconn_debug_attach {pid}`
→ `qconn_debug_break_set {session_id, addr}` → `qconn_debug_continue` (runs to the
breakpoint) → `qconn_debug_read_registers` / `qconn_debug_read_memory` →
`qconn_debug_step` → `qconn_debug_break_clear` → `qconn_debug_detach`.

Debug from `main`: `qconn_debug_launch {path}` (loads the program stopped at its
entry) → `qconn_debug_break_set {session_id, addr: <main>}` → `qconn_debug_continue`
(stops at `main`) → … → `qconn_debug_detach`. The program's stdout/stderr is
captured off the DSMSG text channel.

---

## Build & run

Requires Go ≥ 1.23.

```bash
go build ./...                 # or: go build -o bin/ ./...
go test ./...                  # runs unit + end-to-end tests against the mock
```

Run against the **mock** (no hardware):

```bash
# terminal 1
./bin/mock-qconn -addr 127.0.0.1:8000
# terminal 2
./bin/qconn-mcp --qconn-host 127.0.0.1 --qconn-port 8000 --bind 127.0.0.1:8077
```

Run against a **real QNX 8 target** (start `qconn` on the board first):

```bash
./bin/qconn-mcp --qconn-host 192.168.1.50 --qconn-port 8000 --bind 127.0.0.1:8077
# or: QCONN_HOST=192.168.1.50 ./bin/qconn-mcp
```

In an MCP client (e.g. Claude Code), add an HTTP MCP server pointing at
`http://127.0.0.1:8077/mcp` (plus `Authorization: Bearer <token>` if configured).

### Building target binaries (optional, QNX SDK)

To build QNX target binaries you need the QNX SDK toolchain. On an aarch64 host
the x86-64 SDK tools can be run under [box64](https://github.com/ptitSeb/box64):

```bash
source ~/qnx800/qnxsdp-env.sh
box64 $QNX_HOST/usr/bin/qcc -Vgcc_ntoaarch64le -g -O0 -o myprog myprog.c
```

---

## Configuration

Precedence: **YAML file < environment < flags.** See
[`configs/config.example.yaml`](configs/config.example.yaml).

| Flag | Env | Default | Meaning |
|---|---|---|---|
| `--qconn-host` | `QCONN_HOST` | `127.0.0.1` | target host/IP |
| `--qconn-port` | `QCONN_PORT` | `8000` | target qconn port |
| `--bind` | `MCP_ADDR` | `127.0.0.1:8077` | MCP HTTP bind |
| `--path` | | `/mcp` | MCP mount path |
| `--token` | `MCP_TOKEN` | (none) | require `Authorization: Bearer` |
| `--debug-port` | `QCONN_DEBUG_PORT` | `8001` | port pdebug listens on |
| `--pdebug-cmd` | | `on -d pdebug %d` | pdebug launch template |
| `--debug-serial` | | (none) | serial device for the pdebug transport (e.g. `/dev/ttyUSB1`); empty = TCP |
| `--debug-baud` | | `115200` | baud rate for `--debug-serial` |
| `--timeout` | | `30s` | per-op I/O timeout |
| `--log-level` | `LOG_LEVEL` | `info` | debug/info/warn/error |
| `--log-format` | | `text` | text/json |
| `--trace` | `QCONN_TRACE=1` | `false` | wire hex/ASCII dumps |
| `--config` | `QCONN_CONFIG` | | YAML config path |

---

## Testing

- **Mock (CI / no hardware).** `mock-qconn` implements the greeting, all
  services, canned `pidin` output, a synthetic `/proc/<pid>/as` memory device,
  and a mock DSMSG debug responder, so the full tool surface — including the
  debug session — runs end to end. `go test ./...` drives the client and debug
  bridge against it in-process.
- **Real QNX 8 hardware.** Start `qconn` on the board and point the server at its
  IP. The debug tools (attach, breakpoints, single-step, register/memory
  read/write, threads, detach) are validated against real QNX 8 `pdebug`.

---

## Architecture

```
cmd/
  qconn-mcp/      MCP server entrypoint (Streamable HTTP)
  mock-qconn/     standalone mock target
  qconn-proxy/    logging proxy (development aid)
internal/
  qconn/          qconn protocol client (framing, launcher, file, cntl, info, pidin, /proc)
  qnxdbg/         QNX DSMSG pdebug client (TCP or serial)
  debug/          GDB-RSP codec + pdebug orchestration (gdbserver-style stubs, mock)
  mcpserver/      MCP tool catalog + Streamable HTTP handler + bearer auth
  mockqnx/        reusable mock (broker + launcher + file + cntl + mock debug responder)
  config/         flags + env + YAML config
  obs/            slog logging + wire tracing
test/             end-to-end tests against the in-process mock
```

The MCP layer uses the official Go SDK
(`github.com/modelcontextprotocol/go-sdk`): tools are registered with
`mcp.AddTool` (typed input/output structs → auto-generated JSON schema) and
served via `mcp.NewStreamableHTTPHandler`.

---

## Security

- `qconn` **has no authentication** and executes everything **as root**. Treat
  any reachable `qconn` as a full root shell on the target.
- Bind the MCP server to **localhost** (default) and/or set a **bearer token**
  (`--token`). Put it behind your own TLS/auth proxy if it must be remote.
- `qconn_exec`, `qconn_write_memory`, `qconn_debug_write_memory`, `qconn_signal`,
  `qconn_write_file`, and `qconn_delete` are **destructive/privileged** by nature.
- Use only on development targets you are authorized to access.

---

## Limitations & roadmap

- **Serial pdebug transport** (`--debug-serial`) is implemented; it needs a serial
  line dedicated to pdebug (separate from the system console).
- **Profiling / code coverage / postmortem (coredump)** parity is planned.
- AArch64 register decoding is implemented; x86-64 decoding is a follow-up.
- `pidin` parsers are best-effort and return raw text alongside the parsed fields.

---

## License & provenance

Licensed under the **Apache License, Version 2.0** — see [`LICENSE`](LICENSE)
and [`NOTICE`](NOTICE).

This is an independent implementation of publicly documented QNX interfaces — the
`qconn` target-agent wire protocol and the `pdebug` **DSMSG** debug protocol —
written for **interoperability**. It **contains no QNX source code and no QNX SDK
files**. The implementation is based on public sources: Mandiant's Apache-2.0
[`rpdebug_qnx`](https://github.com/mandiant/rpdebug_qnx), zayfod's MIT
[`qcl`](https://github.com/zayfod/qcl), QNX's public online developer
documentation, the GNU GDB QNX Neutrino target support, and the nmap/Metasploit
`qconn` modules. Protocol constants and message layouts are used as interface
facts.

**Trademarks.** *QNX*, *Neutrino*, and *Momentics* are trademarks of BlackBerry
Limited. This project is **not affiliated with, sponsored by, or endorsed by
BlackBerry Limited**; those names are used only nominatively to describe the
systems this software interoperates with.

---

## Sources & references

- QNX `qconn` utility & IDE host/target docs (QNX 6.4 → 8.0), the System
  Information perspective, `pdebug`, and GDB usage.
- [`zayfod/qcl`](https://github.com/zayfod/qcl) — QNX qconn Perl client (MIT).
- [`mandiant/rpdebug_qnx`](https://github.com/mandiant/rpdebug_qnx) — QNX pdebug
  memory client (Apache-2.0).
- nmap `qconn-exec.nse` and Metasploit `qnx/qconn/qconn_exec` — launcher exec.
- `vocho/openqnx` (QNX ~6.4 sources) — `/proc`, `pidin`, `pdebug`, `fcntl.h`.
- GNU GDB QNX Neutrino target support.
- Official Go MCP SDK: `github.com/modelcontextprotocol/go-sdk`.
