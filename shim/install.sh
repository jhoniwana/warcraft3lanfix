#!/usr/bin/env bash
# Installs the wc3lan LAN shim into a Warcraft III installation.
#
# The shim is loaded as ijl15.dll, because game.dll imports that DLL by
# ordinal. The original file is kept as ijl15_orig.dll and the shim forwards
# every export to it.
set -euo pipefail

GAME_DIR=${1:?usage: $0 "/path/to/WarCraft III"}
HERE=$(cd "$(dirname "$0")" && pwd)

[[ -f "$GAME_DIR/ijl15.dll" ]] || { echo "ijl15.dll not found in $GAME_DIR" >&2; exit 1; }

if [[ ! -f "$GAME_DIR/ijl15_orig.dll" ]]; then
  cp "$GAME_DIR/ijl15.dll" "$GAME_DIR/ijl15_orig.dll"
fi
cp "$HERE/ijl15.dll" "$GAME_DIR/ijl15.dll"
[[ -f "$GAME_DIR/wc3lan.peers" ]] || printf '# One IPv4 per line. Extra peers to send the LAN discovery to.\n' > "$GAME_DIR/wc3lan.peers"

# The release package ships autopeers.sh next to this script. Enable the
# automatic Tailscale peer discovery when it is available.
if [[ -x "$HERE/autopeers.sh" ]]; then
  "$HERE/autopeers.sh" "$GAME_DIR"
fi

echo "installed in $GAME_DIR"
echo "  shim     : $GAME_DIR/ijl15.dll"
echo "  original : $GAME_DIR/ijl15_orig.dll"
echo "  peers    : $GAME_DIR/wc3lan.peers (optional)"
echo "  log      : $GAME_DIR/wc3lan.log"
