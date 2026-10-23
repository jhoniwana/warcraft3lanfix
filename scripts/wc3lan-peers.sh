#!/usr/bin/env bash
# Writes the wc3lan peer list from the local Tailscale state.
#
# Usage: wc3lan-peers "/path/to/game/wc3lan.peers"
#
# The game runs under Wine, and Wine cannot capture the output of the Linux
# tailscale binary, so the list has to be produced outside the game. This
# script is meant to run from a systemd user timer; see autopeers.sh.
set -euo pipefail

OUT=${1:?usage: $0 "/path/to/wc3lan.peers"}

command -v tailscale >/dev/null 2>&1 || exit 0
command -v python3  >/dev/null 2>&1 || exit 0

python3 - "$OUT" <<'PY'
import json
import os
import subprocess
import sys

out = sys.argv[1]
try:
    raw = subprocess.run(["tailscale", "status", "--json"],
                         capture_output=True, text=True, timeout=10).stdout
    state = json.loads(raw)
except Exception:
    sys.exit(0)

me = set(state.get("Self", {}).get("TailscaleIPs", []))
peers = []
for node in state.get("Peer", {}).values():
    for ip in node.get("TailscaleIPs", []):
        if ip.count(".") == 3 and ip not in me and ip not in peers:
            peers.append(ip)

tmp = out + ".tmp"
with open(tmp, "w") as f:
    f.write("# Generated from the local Tailscale state. Do not edit.\n")
    for ip in peers:
        f.write(ip + "\n")
os.replace(tmp, out)
PY
