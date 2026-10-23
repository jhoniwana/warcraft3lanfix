# wc3lan

Play classic **Warcraft III (1.27b)** on networks that are not a plain LAN:
Tailscale, ZeroTier, WireGuard, or two different subnets.

It does not modify the game. It replaces one DLL (`ijl15.dll`) with a small
proxy that copies the LAN discovery packets to the other players' addresses.

## Download

Grab the zip from the [releases page](../../releases):

- `wc3lan-windows.zip` for Windows
- `wc3lan-linux.zip` for Linux with Lutris/Wine

## Windows

1. Install **Tailscale** and log in to the same tailnet as your friends.
2. In your Warcraft III folder, rename `ijl15.dll` to `ijl15_orig.dll`.
3. Copy the `ijl15.dll` from the zip into that folder.
4. Let `war3.exe` through the Windows Firewall (UDP and TCP 6112).
5. Open the game and go to **Local Area Network**.

## Linux (Lutris/Wine)

1. Install **Tailscale** and log in.
2. Run:

   ```bash
   ./install-linux.sh "/path/to/WarCraft III"
   ```

   It installs the shim and enables automatic peer discovery.
3. Start the game with Lutris (Wine runner).
4. Open the game and go to **Local Area Network**.

## Requirements

- Warcraft III **1.27b** (The Frozen Throne), your own copy.
- Tailscale on every machine, logged in to the same tailnet.
- ZeroTier also works without any setup (its network forwards broadcast).
- Windows: allow `war3.exe` through the firewall.

## How it works

The game looks for matches by broadcasting to `255.255.255.255:6112`.
Broadcast never leaves your subnet, so nobody on a VPN can see it.

The proxy hooks the game's own send call and, for every LAN packet, also sends
a copy by **unicast** to each peer (Tailscale addresses, local subnets). The
answers arrive at the game's own socket and from the real peer address, so the
game lists the match and connects normally. No extra program, no proxying, no
port changes.

## Troubleshooting

`wc3lan.log` is created next to `war3.exe`. A healthy start looks like:

```
shim loaded: 7 peers
hooked 1 sendto slot(s) in game.dll
sendto socket bound to 0.0.0.0:6112
```

- Nothing appears: check that both machines are on the same tailnet and that
  `war3.exe` is allowed through the firewall.
- Two copies of the same match appear when both players are on the same WiFi
  **and** on Tailscale. Harmless; it is the same match seen through two paths.
- `could not hook game.dll`: the shim was not loaded. Check that both
  `ijl15.dll` and `ijl15_orig.dll` are in the game folder.

## Uninstall

Copy `ijl15_orig.dll` back over `ijl15.dll` and delete `wc3lan.log`.

## More

- [docs/rea-findings.md](docs/rea-findings.md): reverse engineering notes.
- `cmd/wc3lan`: a Go CLI used during the investigation (not needed to play).
- Build from source: `make` (Go + `i686-w64-mingw32-gcc`).
