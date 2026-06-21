# Project status & validation

Snapshot of what is implemented and how far it has been verified. See
[README.md](README.md) for the full protocol/architecture reference and
[qemu-test/README.md](qemu-test/README.md) for the QEMU findings.

## Implemented (v0.1.0)

- **qconn protocol client** (`internal/qconn`): telnet-aware framing, broker
  handshake, service switching, `info`/`versions`, launcher exec+capture, file
  service (open/read/write/close/delete/chmod, `/proc/<pid>/as` memory),
  cntl signals, pidin-based process/thread/memory/resource introspection.
- **QNX DSMSG / pdebug debug bridge** (`internal/qnxdbg`): full debug over the
  real pdebug protocol — attach, select-thread, continue, single-step,
  breakpoints, register read/write, memory read/write, threads, CPUInfo, detach;
  TCP or serial transport.
- **MCP server** (`internal/mcpserver`): **35 tools** (14 debug) over **Streamable
  HTTP** using the official Go MCP SDK; optional bearer auth; structured logging.
- **mock-qconn** (`internal/mockqnx`) with an embedded pdebug RSP responder.
- **qconn-proxy** (`internal/proxy`): annotated logging proxy (development aid).
- Config (flags/env/YAML), `--trace` wire logging, tests, Makefile.

## Verification matrix

| Area | Status | Evidence |
|---|---|---|
| `go build ./...`, `go vet ./...` | ✅ pass | clean |
| Unit tests (RSP codec, parsers) | ✅ pass | `internal/debug` |
| End-to-end vs mock (incl. debug session) | ✅ pass | `test/` (8 tests) |
| Streamable HTTP transport (initialize → tools/call) | ✅ verified | `qconn_system_info`→`OS=nto`, `qconn_list_processes`→`procnto-smp-instr`, full debug flow attach→continue→regs→mem→detach |
| QNX 6.4.0 boots in QEMU w/ real `qconn` binary | ✅ verified | QEMU 8.2.2; `qconn` runs (pid 57353), `pidin` works |
| qconn **wire protocol** vs a **real QNX 8 target** | ✅ **verified** | RaspberryPi400, QNX 8.0.0 aarch64, qconn 1.4.207944 — see below |
| **Full DSMSG debug** (incl. breakpoints + step) vs real pdebug | ✅ **verified** | RPi400; attach→break→hit→regs r/w→mem r/w→step→detach, direct + via MCP — see below |
| box64 + QNX 8 SDK cross-compile | ✅ verified | `qcc -Vgcc_ntoaarch64le` builds an aarch64 QNX ELF |
| Serial pdebug transport (tty raw + deadlines) | 🟡 partial | termios/open/deadline verified on `/dev/ttyUSB0`; full pdebug-over-serial needs a dedicated line |

### Real-hardware validation (QNX 8.0.0, RaspberryPi400)

Validated end to end over the Streamable HTTP transport against a physical
QNX 8 target (see [support_tools/qnx-rpi400-network.md](support_tools/qnx-rpi400-network.md)).
Working tools: `qconn_system_info`, `qconn_list_services`, `qconn_system_memory`,
`qconn_exec`, `qconn_list_processes` (32 procs), `qconn_process_memory`,
`qconn_resources`, `qconn_memory_map`, `qconn_read_memory` (read ksh's ELF header
`7f454c46…` at its mapped base), `qconn_read_file` (`/etc/hosts`).

Real-protocol fixes this surfaced (the mock alone had not caught them):
- `versions` uses `name=version` (e.g. `launcher=258`), not `name/version`.
- Service prompts are `"<qconn-SVC> "` with a **trailing space** that leaked into
  the next response — now consumed at the source.
- The launcher `start/flags run` is **one-shot**: it streams `OK <pid>` + output
  with **no trailing prompt** and the session ends — so `Exec` now uses a
  dedicated short-lived connection and reads until idle.
- qconn emits telnet `IAC` negotiation mid-stream → IAC handling kept on for the
  whole connection (binary reads bypass it).
- File service errors (`e:<msg>`) and the `pidin mem` `@<hexbase>` segment format
  are now parsed.

## Debug bridge: QNX DSMSG protocol (real pdebug) — FULL FLOW WORKING

QNX `pdebug` does **not** speak GDB RSP. On connect it emits QNX channel framing
(`~\x00\xff~`, 0x7e-delimited) and uses the proprietary **DSMSG** protocol (the
one the QNX-patched gdb speaks). `pdebug <port>` listens on TCP, but must be
started detached (`on -d pdebug <port>`) or the one-shot launcher reaps it.

`internal/qnxdbg` implements that DSMSG protocol (framing, one's-complement
checksum, `~`/`}` escaping, the 0x100-based message counter, and the connect
handshake). The `qconn_debug_*` MCP tools use it.

**Breakpoints and single-step are implemented** from the publicly documented QNX
debug protocol and **validated against real QNX 8 pdebug**:

- **`DStMsg_brk`**: the message **subcmd byte carries the breakpoint type**
  (`_DEBUG_BREAK_EXEC=1`); the body is `size:int32` (0 = set, −1 = remove,
  1..8 = watchpoint span) then `addr:uint64`. (The old `addr[8] size[4]` with
  subcmd 0 was wrong on all three counts.)
- **`DStMsg_run`**: single-step is selected by **subcmd=1** (continue = subcmd 0),
  *not* by a `debug_run` flag — gdb sends `flags=0, tid=1` for both. The body is
  `flags:int32 tid:int32` + 12 reserved bytes.

These layouts match the publicly documented QNX debug constants (`_DEBUG_BREAK_*`,
`_DEBUG_RUN_*`) and are pinned by regression tests
(`internal/qnxdbg/protocol_test.go`).

**Validated end-to-end against real QNX 8 pdebug** (both `cmd/dbgprobe` direct and
via the MCP HTTP tools, debugging a dedicated `test/dbgtarget` binary built with
the QNX SDK):

- `qconn_debug_attach` (spawns pdebug, DSMSG connect/attach/select) and
  `qconn_debug_launch` (load a program stopped at entry → break at `main`)
- `qconn_debug_break_set` → `qconn_debug_continue` → **breakpoint hit** at the
  exact address (`pc` and the function args in `x0/x1` confirmed)
- `qconn_debug_step` — advances exactly one instruction (`pc` += 4)
- `qconn_debug_read_registers` / `qconn_debug_write_registers` — wrote `x0` and
  read it back (`0x284` → `0xdeadbeef`)
- `qconn_debug_read_memory` / `qconn_debug_write_memory`
- `qconn_debug_threads` (Pidlist/TIDNames — process & thread names),
  `qconn_debug_select_thread`, `qconn_debug_target_info` (CPUInfo)
- `qconn_debug_break_clear`, `qconn_debug_detach`

`internal/qnxdbg` is unit-tested (framing/checksum, breakpoint & run frames) and
integration-tested via a mock DSMSG responder (`internal/mockqnx/dsmsg.go`,
now including breakpoint + run-with-stop-notification). `cmd/dbgprobe` drives the
protocol directly (`-brk <addr>` does set→continue→hit→step).

**Serial transport (experimental):** `internal/qnxdbg` is transport-agnostic
(`ConnectSerial`, termios raw mode via `x/sys/unix`); `--debug-serial /dev/ttyX
--debug-baud N` makes the debug tools speak DSMSG over a serial line. The tty
open/raw/read-deadline mechanics are verified on real hardware, but a full
pdebug-over-serial session is **not** hardware-validated here because the board's
only UART is the system console (`/dev/ttyUSB0`); it needs a dedicated serial
line. Use TCP on this board.

**Launch-under-debugger / debug from `main` (DSMSG `Load`) — WORKING.** No file
upload is needed; the program just has to exist on the target. The working
sequence (captured from the SDK gdb on real pdebug) is an **`Env` (cmd 21)
preamble** then `Load`:

- `Env sub=2` resets the environment table, `Env sub=3` appends each
  `"NAME=VALUE"`; `Env sub=0` resets the argv table, `Env sub=1` appends each
  argv entry (the SDK gdb stages the path as both exec-file and argv[0]).
- `Load` body is `argc:int32 envc:int32` (both 0), the program path, then `@0
  @1 @2` markers that reference the staged argv slots. The reply returns the new
  **pid/tid**, with the process stopped before its first instruction. (A bare
  `Load` without the `Env` preamble fails `EINVAL` — the markers have no argv to
  resolve.)

`qconn_debug_launch` implements this. **Validated on real QNX 8 hardware** (via
`cmd/dbgprobe -launch` and the MCP tools): launch a program → it stops at the
loader entry → set a breakpoint at `main` → `continue` stops exactly at `main`
(`pc` and `x0=argc` confirmed). Two fixes were needed for launched processes
(which `attach` did not exercise): the client now filters the DSMSG **text
channel** (the program's stdout/stderr, retrievable via `Client.Output`) so it is
not mistaken for a debug reply, and `SpawnPdebug` redirects pdebug's stdio so a
launched program inherits valid fds instead of the launcher's closing pipe.

Attach-based debugging is unaffected. `Handlesig` (signal disposition table) and
`Mapinfo` remain pending.

The separate `internal/debug` package remains a GDB-RSP bridge for
**gdbserver-style** stubs (tested against the mock RSP responder).

## Toolchain (box64 + QNX 8 SDK)

The QNX 8.0 SDK host tools are x86-64 binaries; on an aarch64 host they run under
**box64**. Verified: cross-compiling an aarch64 QNX ELF with
`qcc -Vgcc_ntoaarch64le`.

## Other remaining gaps

- `write_file` exercised via the mock; `signal` path validated on the real target
  indirectly (cntl service works).
- The openqnx **QEMU** image runs `qconn` but has no TCP/IP stack, so it can't
  serve the wire protocol (the real QNX 8 RaspberryPi400 covers that instead).

## Roadmap

Signal handling (`Handlesig`), shared-library maps (`Mapinfo`); profiling / code
coverage / postmortem (coredump) parity; parser tuning against real QNX 8.
