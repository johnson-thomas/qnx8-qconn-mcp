# QEMU test tier — QNX 8 (built with the QNX SDK)

A throwaway **QNX 8.0** VM in QEMU for exercising `qconn-mcp` without physical
hardware. The image is built from the **QNX Software Development Platform (SDP)
8.0** with its `mkqnximage` tool; QNX 8's `qconn` runs in it and serves the wire
protocol over TCP, so the full MCP tool surface (and the `pdebug` debug path) can
be driven against it.

> **No image is committed to this repo.** You build it locally from your licensed
> QNX SDK. This directory contains only scripts and instructions.

## Prerequisites

- A licensed **QNX SDP 8.0** install (provides `mkqnximage`, `mkifs`, `qcc`, …).
  Source its environment first: `source ~/qnx800/qnxsdp-env.sh`.
- **`qemu-system-x86_64`** on the host (`sudo apt-get install -y qemu-system-x86`).
- The SDK host tools are **x86-64**. On an x86-64 host they run natively. On an
  **aarch64 host**, register an x86-64 emulator so `mkqnximage` can run them — see
  [aarch64 build hosts](#aarch64-build-hosts) below.

## 1. Build the image (outside this repo)

Build in a scratch directory so no image artifacts land in the repo:

```bash
mkdir -p ~/qnx8-qemu && cd ~/qnx8-qemu
source ~/qnx800/qnxsdp-env.sh
mkqnximage --type=qemu --arch=x86_64 --ssh-ident=$HOME/.ssh/id_ed25519.pub --noprompt
# produces output/disk-qemu.vmdk and output/ifs.bin
```

## 2. Boot it

[`run-qnx.sh`](run-qnx.sh) boots the `mkqnximage` output with user-mode
networking and host→guest port-forwards (no root/bridge needed):

```bash
IMGDIR=~/qnx8-qemu/output ./run-qnx.sh
# qconn  -> host 127.0.0.1:18000
# pdebug -> host 127.0.0.1:18001   (for the debug tools)
# ssh    -> host 127.0.0.1:18022
```

The VM boots headless (serial log in `$IMGDIR/qemu.out`); QNX auto-starts `qconn`.

## 3. Drive qconn-mcp against it

```bash
QCONN=127.0.0.1:18000 ./live-smoke.sh
# or run the server directly (debug port must match the forward):
./bin/qconn-mcp --qconn-host 127.0.0.1 --qconn-port 18000 --debug-port 18001 --bind 127.0.0.1:8077
```

Verified working against this VM: `qconn_system_info` (→ `OS=nto RELEASE=8.0.0
CPU=x86_64`), `qconn_system_memory`, `qconn_exec` (`uname -a`, `pidin`),
`qconn_list_processes`, and the DSMSG debug path (`qconn_debug_attach` →
`qconn_debug_read_registers` / `qconn_debug_mapinfo` → `qconn_debug_detach`).

> The decoded registers from `qconn_debug_read_registers` use the **AArch64**
> layout, so on this **x86-64** guest the decoded fields are not meaningful (the
> raw block and the DSMSG transport are correct). x86-64 register decoding is a
> follow-up.

## aarch64 build hosts

The QNX SDK host tools are x86-64 only. To run them on an aarch64 Linux host,
register an x86-64 binfmt handler. **box64** works (it has built-in libc
translation, unlike `qemu-user`, which would also need an x86-64 glibc tree):

```bash
sudo sh -c 'echo ":box64:M::\x7fELF\x02\x01\x01\x00\x00\x00\x00\x00\x00\x00\x00\x00\x02\x00\x3e\x00:\xff\xff\xff\xff\xff\xfe\xfe\x00\xff\xff\xff\xff\xff\xff\xff\xff\xfe\xff\xff\xff:'"$(command -v box64)"':F" > /proc/sys/fs/binfmt_misc/register'
```

`mkqnximage` then runs unchanged. (The VM itself is x86-64 and runs under the
native `qemu-system-x86_64`; box64 is only for the build tools.)

## Why x86-64 and not aarch64?

`mkqnximage --arch=aarch64le` builds everything but fails at the IFS step with
`Host file 'startup-qemu-virt' not available`: the **base SDP 8.0 ships board
startup binaries for x86-64 only** (`startup-x86`, `startup-apic`); the aarch64
QEMU `virt` startup comes from a separate **BSP** package (installed via QNX
Software Center, license required). With such a BSP added,
`mkqnximage --type=qemu --arch=aarch64le --aarch64-version=8` would produce an
aarch64 image that `qemu-system-aarch64` can run (with KVM on an aarch64 host).
