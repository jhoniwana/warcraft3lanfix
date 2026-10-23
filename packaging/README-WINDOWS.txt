wc3lan - Warcraft III LAN over Tailscale, ZeroTier or WireGuard
===============================================================

1. Install Tailscale and log in to the same tailnet as your friends.
2. In your Warcraft III folder, rename ijl15.dll to ijl15_orig.dll.
3. Copy the ijl15.dll from this zip into that folder.
4. Allow war3.exe in Windows Firewall (UDP and TCP, port 6112).
5. Open the game and go to Local Area Network (Red de Area Local).

The game must be 1.27b (build 1.27.1.7085) and must be your own copy.

Antivirus: this replaces a DLL in the game folder, so some tools may report a
false positive. The source is on GitHub and you can build it yourself.

wc3lan.log appears next to war3.exe. It should say:
  hooked 1 sendto slot(s) in game.dll
  sendto socket bound to 0.0.0.0:6112

Uninstall: copy ijl15_orig.dll back over ijl15.dll and delete wc3lan.log.
