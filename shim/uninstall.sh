#!/usr/bin/env bash
# Restores the original ijl15.dll and removes the shim files.
set -euo pipefail

GAME_DIR=${1:?usage: $0 "/path/to/WarCraft III"}

[[ -f "$GAME_DIR/ijl15_orig.dll" ]] || { echo "ijl15_orig.dll not found, nothing to restore" >&2; exit 1; }
cp "$GAME_DIR/ijl15_orig.dll" "$GAME_DIR/ijl15.dll"
rm -f "$GAME_DIR/wc3lan.log"
echo "restored $GAME_DIR/ijl15.dll"
