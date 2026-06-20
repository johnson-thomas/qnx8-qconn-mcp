#!/bin/sh
#
# QNX 8 RPi400 network bring-up (fixed).
#
# Why this exists: the stock start_net.sh runs `if_up -r 20 genet0` and waits for
# a link, but the genet driver's autonegotiation can stick at
# "media: Ethernet autoselect (none)" so the link never comes up — DHCP then gets
# no lease and the board has no network until someone forces the media by hand
# over the serial console. This version forces the media (1000baseT full-duplex)
# and retries until the link is active, so the network comes up unattended.
#
# Install on the target (the /system partition is writable and persistent):
#   cp /system/bin/start_net.sh /system/bin/start_net.sh.orig   # once
#   cp start_net.sh /system/bin/start_net.sh
# Static IP comes from /data/var/etc/settings/network (IP_ADDR=192.168.2.10).

# --- genet0 (wired) bring-up: force media + retry until the link is active ---
ifconfig genet0 up
__try=0
while [ $__try -lt 30 ]; do
	ifconfig genet0 media 1000baseT mediaopt full-duplex 2>/dev/null
	# Link is good once the media reports 1000baseT and is no longer "none".
	if ifconfig genet0 | grep -q '1000baseT' && ! ifconfig genet0 | grep -q 'none'; then
		break
	fi
	sleep 1
	__try=$((__try + 1))
done
if_up -p -r 20 genet0

# --- bcm0 (wifi) unchanged from the stock script ---
if_up -p -r 20 bcm0
ifconfig bcm0 up

wpa_supplicant-2.11 -Dqwdi -t -Z100 -i bcm0 -c /data/var/etc/settings/wpa_supplicant.conf &

IP_ADDR=dhcp
HOSTNAME=noname

. /data/var/etc/settings/network

setconf _CS_HOSTNAME ${HOSTNAME}

if [ "$IP_ADDR" != dhcp ]; then
	# genet0 is the wired interface; apply a /24 for the direct host link.
	ifconfig genet0 $IP_ADDR netmask 255.255.255.0
fi

sysctl -w net.inet.icmp.bmcastecho=1 > /dev/null
sysctl -w qnx.sec.droproot=33:33 > /dev/null

# If dhcpcd not run as root, need to give it read/write access to /dev/bpf
setfacl -m user:38:rw  /dev/bpf

if [ "$IP_ADDR" = dhcp ]; then
	 dhcpcd -bq -f /system/etc/dhcpcd/dhcpcd.conf -c /system/etc/dhcpcd/dhcpcd-run-hooks
else
	/system/etc/startup/pinger.sh delay &
fi

exit 0
