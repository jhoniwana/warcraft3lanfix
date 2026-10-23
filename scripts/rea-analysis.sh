#!/usr/bin/env bash
#
# Reverse engineering of the Warcraft III classic client with REA.
#
# It looks for the LAN discovery code of the client: the place where the
# "query for LAN game" packet (opcode 0x2F) is built and sent to the
# broadcast address on the UDP port 6112. The output is a worklist of
# candidate addresses that can then be decompiled with:
#
#   rea function  <Game.exe> <address> --provider ghidra --json
#   rea decompile <Game.exe> <address> --provider ghidra --json
#
# Usage: scripts/rea-analysis.sh /path/to/Game.exe
#
set -euo pipefail

export GHIDRA_INSTALL_DIR=${GHIDRA_INSTALL_DIR:-/home/jhon/tools/ghidra_12.1.4_PUBLIC}
export JAVA_HOME=${JAVA_HOME:-/home/jhon/.jdks/jdk-21.0}
PROVIDER=${PROVIDER:-ghidra}

if [[ $# -lt 1 ]]; then
  echo "usage: $0 /path/to/Game.exe" >&2
  exit 2
fi
TARGET=$(readlink -f "$1")
OUT=${OUT:-docs/rea-raw}
mkdir -p "$OUT"
BASE=$(basename "$TARGET")

echo "==> target: $TARGET"
file "$TARGET" || true
sha256sum "$TARGET" | tee "$OUT/$BASE.sha256"

echo "==> analysing with the $PROVIDER provider (this takes a while)"
rea analyze "$TARGET" --provider "$PROVIDER" --format json >"$OUT/$BASE.analyze.json"

echo "==> searching for the broadcast address (FF FF FF FF)"
rea search-bytes "$TARGET" "ffffffff" --provider "$PROVIDER" --limit 400 \
  --format json >"$OUT/$BASE.bytes.ffffffff.json"

echo "==> searching for the discovery port 6112 (E0 17)"
rea search-bytes "$TARGET" "e017" --provider "$PROVIDER" --limit 400 \
  --format json >"$OUT/$BASE.bytes.e017.json"

echo "==> searching for the query opcode 0x2F"
rea search-bytes "$TARGET" "??2f" --provider "$PROVIDER" --limit 400 \
  --format json >"$OUT/$BASE.bytes.2f.json"

echo "==> searching the strings for the LAN wording"
for s in "LAN" "LAN Game" "6112" "Game" "W3XP" "1.27"; do
  safe=$(echo "$s" | tr -c 'A-Za-z0-9' '_')
  rea search "$TARGET" --pattern "$s" --mode literal --provider "$PROVIDER" \
    --format json >"$OUT/$BASE.str.$safe.json" || true
done

echo "==> strings that mention the network"
rea search "$TARGET" --pattern "6112" --mode literal --provider "$PROVIDER" \
  --format json >"$OUT/$BASE.str.port.json" || true

echo
echo "Results in $OUT"
ls -la "$OUT"
