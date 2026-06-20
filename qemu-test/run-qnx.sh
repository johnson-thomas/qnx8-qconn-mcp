#!/usr/bin/env bash
#
# Boot the openqnx QNX Neutrino 6.4.0 image (qnxfs.img) in QEMU with an NE2000
# NIC and host->guest port-forward for qconn (TCP 8000).
#
# Console: defaults to a curses (text) VGA console rendered in this terminal,
# so it works over SSH. Set DISPLAY_MODE=vnc to expose the console on VNC :0
# (connect a VNC viewer to 127.0.0.1:5900) or DISPLAY_MODE=gtk for a window.
#
# After boot you land at the QNX 'esh' shell. Networking is NOT auto-started in
# this image, so bring it up and start qconn manually (see qemu-test/README.md):
#
#     io-pkt-v4 -dne2000
#     ifconfig en0 10.0.2.15 up
#     qconn
#
# Then, on the HOST, qconn is reachable at 127.0.0.1:8000.
set -euo pipefail
cd "$(dirname "$0")"

QEMU="${QEMU:-qemu-system-i386}"
IMG="${IMG:-qnxfs.img}"
HOSTPORT="${HOSTPORT:-8000}"
MEM="${MEM:-256}"
DISPLAY_MODE="${DISPLAY_MODE:-curses}"

command -v "$QEMU" >/dev/null 2>&1 || {
  echo "ERROR: $QEMU not found. Install it, e.g.:  sudo apt-get install -y qemu-system-x86" >&2
  exit 1
}

case "$DISPLAY_MODE" in
  curses) DISP=(-display curses) ;;
  vnc)    DISP=(-vnc :0) ;;
  gtk)    DISP=(-display gtk) ;;
  none)   DISP=(-nographic) ;;
  *)      DISP=(-display "$DISPLAY_MODE") ;;
esac

echo "Booting $IMG via $QEMU (console=$DISPLAY_MODE, host qconn port=$HOSTPORT)"
echo "Tip: in curses mode, press Esc-2 / Esc-1 to switch monitor/console; Ctrl-a x is NOT used here."

exec "$QEMU" \
  -hda "$IMG" \
  -m "$MEM" \
  -device ne2k_pci,netdev=n0 \
  -netdev "user,id=n0,hostfwd=tcp::${HOSTPORT}-:8000" \
  "${DISP[@]}"
