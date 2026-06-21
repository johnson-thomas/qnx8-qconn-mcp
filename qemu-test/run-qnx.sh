#!/usr/bin/env bash
#
# Boot a QNX 8 image built with the QNX SDK's mkqnximage (see README.md) in QEMU
# with user-mode networking and host->guest port-forwards — no root/bridge.
#
# Build first (outside this repo):
#     mkdir -p ~/qnx8-qemu && cd ~/qnx8-qemu
#     source ~/qnx800/qnxsdp-env.sh
#     mkqnximage --type=qemu --arch=x86_64 --noprompt
#
# Then:
#     IMGDIR=~/qnx8-qemu/output ./run-qnx.sh
#
# Forwards (override via env): qconn 18000->8000, pdebug 18001->18001, ssh 18022->22.
# The guest auto-starts qconn; on the HOST it is reachable at 127.0.0.1:$QCONN_PORT.
set -euo pipefail
cd "$(dirname "$0")"

IMGDIR="${IMGDIR:-$HOME/qnx8-qemu/output}"
QEMU="${QEMU:-qemu-system-x86_64}"
MEM="${MEM:-1G}"
SMP="${SMP:-2}"
QCONN_PORT="${QCONN_PORT:-18000}"   # host port -> guest 8000 (qconn)
DEBUG_PORT="${DEBUG_PORT:-18001}"   # host port -> guest 18001 (pdebug; pass --debug-port 18001 to qconn-mcp)
SSH_PORT="${SSH_PORT:-18022}"       # host port -> guest 22

command -v "$QEMU" >/dev/null 2>&1 || {
  echo "ERROR: $QEMU not found. Install it, e.g.:  sudo apt-get install -y qemu-system-x86" >&2
  exit 1
}
[ -f "$IMGDIR/disk-qemu.vmdk" ] || { echo "ERROR: $IMGDIR/disk-qemu.vmdk not found — build with mkqnximage first (see README.md)." >&2; exit 1; }
[ -f "$IMGDIR/ifs.bin" ] || { echo "ERROR: $IMGDIR/ifs.bin not found — build with mkqnximage first." >&2; exit 1; }

# KVM only helps when host and guest arch match (i.e. an x86-64 host). On an
# aarch64 host this x86-64 guest runs under TCG (slower) automatically.
CPU=(-cpu max)
if [ -w /dev/kvm ] && [ "$(uname -m)" = "x86_64" ]; then CPU=(-enable-kvm -cpu host); fi

echo "Booting QNX 8 ($IMGDIR) — qconn=127.0.0.1:$QCONN_PORT pdebug=127.0.0.1:$DEBUG_PORT ssh=127.0.0.1:$SSH_PORT"
echo "Serial log: $IMGDIR/qemu.out"

exec "$QEMU" -smp "$SMP" "${CPU[@]}" -m "$MEM" \
  -drive file="$IMGDIR/disk-qemu.vmdk",if=ide,id=drv0 \
  -kernel "$IMGDIR/ifs.bin" \
  -netdev "user,id=net0,hostfwd=tcp::${QCONN_PORT}-:8000,hostfwd=tcp::${DEBUG_PORT}-:${DEBUG_PORT},hostfwd=tcp::${SSH_PORT}-:22" \
  -device virtio-net-pci,netdev=net0 \
  -object rng-random,filename=/dev/urandom,id=rng0 -device virtio-rng-pci,rng=rng0 \
  -display none -serial "file:$IMGDIR/qemu.out" -monitor none -pidfile "$IMGDIR/qemu.pid"
