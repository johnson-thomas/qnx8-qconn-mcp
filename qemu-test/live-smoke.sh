#!/usr/bin/env bash
#
# Drive qconn-mcp against a LIVE qconn (the QEMU guest, or a real QNX target) and
# exercise a few tools over the Streamable HTTP transport.
#
# Prereqs: qconn reachable at $QCONN (default 127.0.0.1:8000, i.e. the QEMU
# hostfwd) and the qconn-mcp binary built (run `make build` in the repo root).
#
# Usage:  ./live-smoke.sh            # uses 127.0.0.1:8000
#         QCONN=192.168.1.50:8000 ./live-smoke.sh
set -euo pipefail
cd "$(dirname "$0")/.."

QCONN="${QCONN:-127.0.0.1:8000}"
HOST="${QCONN%:*}"; PORT="${QCONN##*:}"
BIND="127.0.0.1:8077"
ACCEPT='Accept: application/json, text/event-stream'
CT='Content-Type: application/json'

[ -x bin/qconn-mcp ] || go build -o bin/ ./...

echo ">> launching qconn-mcp against $HOST:$PORT (trace on)"
./bin/qconn-mcp --qconn-host "$HOST" --qconn-port "$PORT" --bind "$BIND" \
  --log-level debug --trace >/tmp/qconn-mcp-live.log 2>&1 &
MCP=$!
trap 'kill $MCP 2>/dev/null' EXIT
sleep 1

H=$(mktemp)
curl -s -D "$H" -H "$ACCEPT" -H "$CT" -X POST "http://$BIND/mcp" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"live","version":"0"}}}' >/dev/null
SID=$(grep -i '^Mcp-Session-Id:' "$H" | tr -d '\r' | awk '{print $2}')
call(){ curl -s -H "$ACCEPT" -H "$CT" -H "Mcp-Session-Id: $SID" -X POST "http://$BIND/mcp" -d "$1"; }
call '{"jsonrpc":"2.0","method":"notifications/initialized"}' >/dev/null

echo "== qconn_system_info =="
call '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"qconn_system_info","arguments":{}}}'
echo; echo "== qconn_list_services =="
call '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"qconn_list_services","arguments":{}}}'
echo; echo "== qconn_exec: uname -a =="
call '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"qconn_exec","arguments":{"command":"uname -a"}}}'
echo; echo "== qconn_list_processes =="
call '{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"qconn_list_processes","arguments":{}}}'
echo
echo ">> wire trace saved to /tmp/qconn-mcp-live.log (use it to validate parsers vs real output)"
