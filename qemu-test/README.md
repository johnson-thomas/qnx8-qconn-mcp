# QEMU test tier (openqnx QNX Neutrino 6.4.0)

This directory boots the **prebuilt openqnx image** (`qnxfs.img` / `qnx.ifs`,
from `github.com/vocho/openqnx`) under QEMU. The image **boots QNX 6.4.0 and
contains the `qconn` and `pidin` binaries**, so it is the closest open, no-cost
target to a real QNX box.

## Boot it

```bash
./run-qnx.sh                 # curses VGA console in this terminal
# DISPLAY_MODE=vnc ./run-qnx.sh   # console on VNC :0 (127.0.0.1:5900)
```

Or drive it non-interactively via tmux (what the automated check uses):

```bash
tmux new-session -d -s qnx -x 80 -y 25
tmux send-keys -t qnx "qemu-system-i386 -drive file=qnxfs.img,format=raw -m 256 \
  -device ne2k_pci,netdev=n0 -netdev user,id=n0,hostfwd=tcp::18000-:8000 \
  -display curses" Enter
sleep 18
tmux capture-pane -t qnx -p        # see the boot log + '#' esh prompt
# send commands (one per line; esh does NOT support ';' or '&&'):
tmux send-keys -t qnx "pidin" Enter
tmux kill-session -t qnx           # stop the VM
```

`/proc/boot` contains: `procnto-instr`, `devb-eide`, `devc-con`, `devc-pty`,
`devn-ne2000.so`, `libsocket.so`, `ifconfig`, `ping`, `dhcp.client`, `tftp`,
`tftpd`, **`qconn`**, **`pidin`**.

## Findings (2026-06: actual run on this machine)

QEMU 8.2.2, `qemu-system-i386`, image booted to the `esh` shell. Verified:

- ✅ **QNX 6.4.0 boots** and reaches `# ` (esh).
- ✅ **`qconn` runs**: `qconn` then `pidin` shows pid 57353 with 4 threads in the
  normal idle states (`SIGWAITINFO`/`CONDVAR`/`RECEIVE`).
- ✅ **`pidin` works** and prints a real process/thread listing.
- ❌ **qconn is NOT reachable over TCP.** A host connection to the forwarded port
  is accepted by QEMU but the guest never sends the `QCONN` banner, and `/dev`
  has **no socket/network resource manager** (no `io-net`, no `io-pkt`, no
  `en0`, no `/dev/socket`). The boot log confirms it: `Unable to start "io-net"
  (2)` (ENOENT) — the image ships the NE2000 *driver* (`devn-ne2000.so`) and
  `libsocket`, but **no TCP/IP stack binary**.

**Conclusion:** this open image cannot exercise the qconn *wire protocol*,
because qconn needs a TCP/IP stack (`io-net` on 6.4) that is a **closed QNX
binary absent from openqnx**. It can't be injected without a network (the guest
has `tftp`/`tftpd` but no stack to use them — chicken-and-egg) or the QNX
`mkifs` tooling to rebuild the IFS.

So the MCP server's protocol has been validated end-to-end against the
[mock](../internal/mockqnx) (Tier A), and this Tier B confirms a real `qconn`
binary boots and runs — but full wire validation needs one of:

1. An `io-net` (QNX 6.4) binary from a **Momentics 6.4** install added to the
   image (then start it at the esh prompt: `io-net -di ne2000`, `ifconfig en0
   10.0.2.15 up`, `qconn`), or a Momentics-built IFS that includes `io-pkt`.
2. A **real QNX 8 target** (or QNX 7.x eval image) running `qconn`.

Once a live qconn is reachable, run the wire check:

```bash
QCONN=127.0.0.1:18000 ./live-smoke.sh   # drives qconn-mcp tools over Streamable HTTP
```

The `--trace` logs (written to `/tmp/qconn-mcp-live.log`) and `qconn-proxy`
help validate the `pidin` parsers and the pdebug launch model against real
output.

## Provenance

Images copied from `vocho/openqnx` @ master:

```
qnxfs.img  sha256 9571161f47c059124a1fca8427c1a3322bebfc7032e186bcbf585d2c5f94a6db
qnx.ifs    sha256 b169376e9a9cc914035068a7133c4e6a1d66d5755f498775532f1cd0a300cdb6
```

(Not committed — see `.gitignore`. Re-download from openqnx if needed.)
