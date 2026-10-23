#!/usr/bin/env bash
# Repairs a Wine prefix after moving it to a different path or user.
#
# Usage: fix-prefix.sh "/path/to/prefix"
set -euo pipefail

PREFIX=${1:?usage: $0 "/path/to/prefix"}
D="$PREFIX/dosdevices"

[[ -d "$PREFIX/drive_c" ]] || { echo "prefix not found: $PREFIX" >&2; exit 1; }

ln -sfn ../drive_c "$D/c:"
ln -sfn / "$D/z:"
ln -sfn /mnt "$D/w:"
ln -sfn "$HOME" "$D/x:"

# If the prefix was created by another user, copy their profile so the game
# keeps its settings and saves.
CUR=$(id -un)
UDIR="$PREFIX/drive_c/users"
if [[ -d "$UDIR" && ! -e "$UDIR/$CUR" ]]; then
  for src in "$UDIR"/*; do
    name=$(basename "$src")
    case "$name" in
      Public|Default|"$CUR"|"$CUR".*) continue ;;
    esac
    if [[ -d "$src" ]]; then
      cp -a "$src" "$UDIR/$CUR"
      echo "profile copied from $name to $CUR"
      break
    fi
  done
fi

echo "prefix ready: $PREFIX"
