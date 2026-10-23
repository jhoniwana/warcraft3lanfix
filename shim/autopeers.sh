#!/usr/bin/env bash
# Installs a systemd user timer that keeps wc3lan.peers up to date from the
# local Tailscale state, so nobody has to edit the list by hand.
#
# Usage: autopeers.sh "/path/to/WarCraft III"
set -euo pipefail

GAME=${1:?usage: $0 "/path/to/WarCraft III"}
HERE=$(cd "$(dirname "$0")" && pwd)
BIN="$HOME/.local/bin/wc3lan-peers"
UNIT_DIR="$HOME/.config/systemd/user"

SOURCE=""
for cand in "$HERE/wc3lan-peers.sh" "$HERE/../scripts/wc3lan-peers.sh"; do
  if [[ -f "$cand" ]]; then SOURCE=$cand; break; fi
done
[[ -n "$SOURCE" ]] || { echo "wc3lan-peers.sh not found next to $0" >&2; exit 1; }

mkdir -p "$(dirname "$BIN")"
install -m 755 "$SOURCE" "$BIN"

# Fill the list right away.
"$BIN" "$GAME/wc3lan.peers" || true

if command -v systemctl >/dev/null 2>&1 && systemctl --user show-environment >/dev/null 2>&1; then
  mkdir -p "$UNIT_DIR"
  cat > "$UNIT_DIR/wc3lan-peers.service" <<EOF
[Unit]
Description=Update the Warcraft III LAN peer list from Tailscale

[Service]
Type=oneshot
ExecStart=$BIN "$GAME/wc3lan.peers"
EOF
  cat > "$UNIT_DIR/wc3lan-peers.timer" <<EOF
[Unit]
Description=Refresh the Warcraft III LAN peer list periodically

[Timer]
OnBootSec=20s
OnUnitActiveSec=30s

[Install]
WantedBy=timers.target
EOF
  systemctl --user daemon-reload
  systemctl --user enable --now wc3lan-peers.timer
  systemctl --user start wc3lan-peers.service || true
  echo "automatic peer discovery enabled (systemd user timer)"
else
  echo "systemd user session not available; run $BIN \"$GAME/wc3lan.peers\" manually"
fi
