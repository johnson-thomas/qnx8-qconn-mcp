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

## Post-boot bring-up

The image is configured for DHCP and runs the interface bring-up
(`/system/bin/start_net.sh`) too early in boot (link not ready). A helper was
installed on the writable `/data` partition:

```sh
# /data/netup.sh
ifconfig genet0 up
ifconfig genet0 media 1000baseT mediaopt full-duplex   # the fix
ifconfig genet0 192.168.2.10 netmask 255.255.255.0
```

After each boot, run it over the serial console:

```sh
sh /data/netup.sh
```

Then qconn is reachable at `192.168.2.10:8000` and the MCP server can be pointed
at it:

```bash
./bin/qconn-mcp --qconn-host 192.168.2.10 --qconn-port 8000 --bind 127.0.0.1:8077
# verify: python3 -c "import socket;s=socket.create_connection(('192.168.2.10',8000));print(s.recv(16))"  # -> b'QCONN\r\n'
```

(Reboot the board from the serial console with `shutdown`, which reboots; the SD
image then needs `sh /data/netup.sh` again. The host side is persistent.)

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
