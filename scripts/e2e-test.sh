#!/usr/bin/env bash
#
# End to end test of the discovery protocol, the bridge and the proxy.
#
# It uses the fake host and the fake client of the wc3lan tool, so no copy
# of Warcraft III is needed. The three processes run on the loopback
# interface with different addresses, which reproduces the situation of three
# different machines:
#
#   127.0.0.1:6112  the machine that hosts the game
#   127.0.0.1:6113  the bridge (a player with an unmodified game)
#   127.0.0.2:6112  the player with the patched game
#
set -euo pipefail

cd "$(dirname "$0")/.."
BIN=${BIN:-/tmp/wc3lan}
LOGDIR=$(mktemp -d /tmp/wc3lan-e2e.XXXXXX)

cleanup() {
  for pid in ${PIDS:-}; do kill "$pid" 2>/dev/null || true; done
  wait 2>/dev/null || true
  if [[ ${KEEP_LOGS:-0} != 1 ]]; then rm -rf "$LOGDIR"; else echo "logs: $LOGDIR"; fi
}
PIDS=()
trap cleanup EXIT

echo "==> building"
go build -o "$BIN" ./cmd/wc3lan

echo "==> starting the fake host on 127.0.0.1:6112"
"$BIN" sim-host --listen 127.0.0.1:6112 --name "E2E Game" >"$LOGDIR/host.log" 2>&1 &
PIDS+=($!)
sleep 0.5

echo "==> probe: asking the host for its game"
if ! "$BIN" probe --host 127.0.0.1 --udp-port 6112 >"$LOGDIR/probe.log" 2>&1; then
  echo "FAIL: probe did not get an answer"
  cat "$LOGDIR/probe.log"
  exit 1
fi
cat "$LOGDIR/probe.log"
grep -q "E2E Game" "$LOGDIR/probe.log" || {
  echo "FAIL: the game name did not arrive"
  exit 1
}
grep -q "game port : 6112" "$LOGDIR/probe.log" || {
  echo "FAIL: the game port did not arrive"
  exit 1
}

echo "==> starting the bridge on 127.0.0.1:6113"
"$BIN" bridge --host 127.0.0.1 --udp-port 6112 --proxy-port 6113 \
  --broadcast 127.0.0.2:6112 >"$LOGDIR/bridge.log" 2>&1 &
PIDS+=($!)
sleep 1.5

echo "==> starting the fake client on 127.0.0.2:6112"
"$BIN" sim-client --bind 127.0.0.2:6112 --broadcast 127.0.0.1:6112 \
  --count 2 --wait 3s >"$LOGDIR/client.log" 2>&1 || true
cat "$LOGDIR/client.log"
grep -q "E2E Game" "$LOGDIR/client.log" || {
  echo "FAIL: the client did not see the game"
  exit 1
}

echo "==> checking the TCP proxy"
"$BIN" sim-client --bind 127.0.0.2:6112 --broadcast 127.0.0.1:6112 --count 1 --wait 1s >/dev/null 2>&1 || true

echo "==> starting the host gateway on 127.0.0.3:6122"
"$BIN" gateway --game 127.0.0.1:6112 --listen 127.0.0.3:6122 \
  --poll 500ms --announce 500ms >"$LOGDIR/gateway.log" 2>&1 &
PIDS+=($!)
sleep 1.5

echo "==> a remote client that only knows the gateway"
"$BIN" sim-client --bind 127.0.0.4:6112 --broadcast 127.0.0.3:6122 \
  --count 2 --wait 3s >"$LOGDIR/client2.log" 2>&1 || true
cat "$LOGDIR/client2.log"
grep -q "E2E Game" "$LOGDIR/client2.log" || {
  echo "FAIL: the client did not see the game through the gateway"
  cat "$LOGDIR/gateway.log"
  exit 1
}

echo
echo "ALL CHECKS PASSED"
