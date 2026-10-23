wc3lan - Warcraft III LAN over Tailscale, ZeroTier or WireGuard
===============================================================

1. Install Tailscale and log in to the same tailnet as your friends.
2. Run:
     ./install-linux.sh "/path/to/WarCraft III"
   It installs the shim and enables automatic peer discovery through a
   systemd user timer that rewrites wc3lan.peers every 30 seconds.
3. Start the game with Lutris using a Wine runner.
4. Open the game and go to Local Area Network (Red de Area Local).

Without systemd, run
  ~/.local/bin/wc3lan-peers "/path/to/WarCraft III/wc3lan.peers"
whenever the tailnet changes.

The game must be 1.27b (build 1.27.1.7085) and must be your own copy.

wc3lan.log appears next to war3.exe. It should say:
  hooked 1 sendto slot(s) in game.dll
  sendto socket bound to 0.0.0.0:6112

Uninstall: copy ijl15_orig.dll back over ijl15.dll and delete wc3lan.log.
