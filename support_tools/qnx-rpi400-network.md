# QNX 8 RaspberryPi400 — serial console + network bring-up

Notes for the physical QNX 8.0.0 RaspberryPi400 target used to validate the
qconn MCP server against real hardware.

## Topology

- **Serial console:** FTDI USB-serial at `/dev/ttyUSB0` (115200 8N1) → RPi400 GPIO
  UART. Driven with `support_tools/bpi_console.py`.
- **Network:** RPi400 onboard ethernet (`genet0`) ↔ host **onboard** NIC
  (`enP7s7`), direct link, subnet **192.168.2.0/24**:
  - Host `enP7s7` = `192.168.2.1/24` (NetworkManager "Wired connection 3",
    `ipv4.method=manual` — persistent).
  - RPi `genet0`  = `192.168.2.10/24`.
- qconn listens on `*.8000` (auto-started at boot by `/proc/boot/startup.sh`).

Use the host's onboard port, NOT a USB-ethernet dongle.

## Serial usage

```bash
# system python has pyserial; the vllm venv python does not
/usr/bin/python3 support_tools/bpi_console.py --port /dev/ttyUSB0 --cmd "uname -a"
```

## The genet link gotcha (and the fix)

On this board, QNX's `genet` driver autonegotiation gets **stuck**: `ifconfig
genet0` shows `media: Ethernet autoselect (none)` and the interface passes **zero
packets**, even though the host NIC reports a healthy `1000Mb/s` link. Symptoms:
`if_up: tries exhausted` at boot, `ping: No route to host`, no IPv4 route
installed.

**Fix — force the media to match the host (gigabit full-duplex):**

```sh
ifconfig genet0 media 1000baseT mediaopt full-duplex
```

This immediately flips genet0 to `status: active` and the link works
bidirectionally.

## Permanent fix (no manual step at boot)

The stock `/system/bin/start_net.sh` does `if_up -r 20 genet0` and waits for a
link that never comes up (the stuck autoneg above), and defaults to DHCP — which
has no server on this direct link. The result was that the network had to be
brought up by hand over the serial console after every boot.

This is fixed permanently by replacing `start_net.sh` (the `/system` partition is
writable and persistent) with [`start_net.sh`](start_net.sh) in this directory,
which **forces the media and retries until the link is active**, then applies the
static IP. Install it on the target:

```sh
cp /system/bin/start_net.sh /system/bin/start_net.sh.orig   # once, to keep a backup
# copy support_tools/start_net.sh to the board, then:
cp /tmp/start_net.sh.new /system/bin/start_net.sh
chmod +x /system/bin/start_net.sh
# static IP, read by start_net.sh from the (persistent) settings file:
sed -i 's/^IP_ADDR=.*/IP_ADDR=192.168.2.10/' /data/var/etc/settings/network
```

`/data/var/etc/settings/network` is only overwritten by `copy_settings.sh` when
`/boot/network` is **newer** (`-nt`), and `/boot/network` carries no `IP_ADDR`
line, so this static IP persists across reboots.

**Verified:** after `shutdown` (which reboots), the board comes up with
`192.168.2.10` and `media 1000baseT <full-duplex>` automatically — no serial
intervention — and qconn is reachable:

```bash
./bin/qconn-mcp --qconn-host 192.168.2.10 --qconn-port 8000 --bind 127.0.0.1:8077
# verify: python3 -c "import socket;s=socket.create_connection(('192.168.2.10',8000));print(s.recv(16))"  # -> b'QCONN\r\n'
```

The host side (`enP7s7` = `192.168.2.1/24`) is already persistent.

## Serial console as recovery / fallback

The `/dev/ttyUSB0` console is the fallback path when the network qconn is wedged.
Use it to restart services or re-run the netup helper, e.g.:

```bash
/usr/bin/python3 support_tools/bpi_console.py --port /dev/ttyUSB0 --cmd "slay -f qconn; qconn &"
/usr/bin/python3 support_tools/bpi_console.py --port /dev/ttyUSB0 --cmd "sh /data/netup.sh"
```

## pdebug over serial (transport option)

`qconn-mcp --debug-serial /dev/ttyX --debug-baud 115200` makes the debug tools
speak the DSMSG protocol to pdebug over a **serial line** instead of TCP (pdebug
must be started on the target bound to that line, out of band). On this RPi400
the only UART is the console above, so pdebug-over-serial would collide with it —
use the network transport here and keep `/dev/ttyUSB0` for console/recovery. A
second USB-serial adapter dedicated to pdebug is needed to use this path.

## Debugging a target binary (box64 QNX SDK)

```bash
source ~/qnx800/qnxsdp-env.sh && export BOX64_PATH=$QNX_HOST/usr/bin
box64 $QNX_HOST/usr/bin/qcc -Vgcc_ntoaarch64le -g -O0 -o dbgtarget test/dbgtarget/dbgtarget.c
scp -O dbgtarget root@192.168.2.10:/tmp/ && ssh root@192.168.2.10 '/tmp/dbgtarget &'
# then attach via the MCP qconn_debug_* tools (pid from qconn_list_processes)
```
