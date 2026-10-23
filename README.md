# Warcraft III classic LAN over modern networks

Makes the LAN discovery of classic Warcraft III (1.27b) work outside a single
flat subnet: over Tailscale, ZeroTier, WireGuard or any point to point link,
without changing the game files.

## Status

- LAN between two Linux/Wine machines: verified, the two clients see each
  other and join.
- Tailscale between the same two machines: verified end to end. With the LAN
  discovery blocked by firewall rules, the game still found the match and
  opened its session over `100.x`:

  ```
  ESTAB  100.64.0.10:58940  ->  100.64.0.20:6112  (war3.exe)
  ```

- Windows 10 (inside a VM) with Tailscale: verified. The shim loads as
  `ijl15.dll`, finds the peers through `tailscale.exe status --json` and the
  game connects over the tailnet.

## The problem

The client sends its "query for LAN game" packet (opcode `0x2F`) to
`255.255.255.255:6112` and the host answers there. Broadcast does not cross a
Tailscale or WireGuard tunnel, and with several adapters (WiFi, VPN, Docker)
the packet leaves through the wrong one.

## The fix

`shim/` is a small 32 bit Windows DLL that is loaded **inside `war3.exe`**:

1. It replaces the `WS2_32.sendto` slot of `game.dll`'s import table.
2. When the game sends a LAN packet to `255.255.255.255:6112`, the shim also
   sends it, **from the game's own socket**, to every known peer by unicast
   (Tailscale addresses, ZeroTier, each local subnet broadcast, extra IPs from
   `wc3lan.peers`).
3. The answers arrive at the same socket and from the real peer address, so
   the game lists the match and opens its TCP session straight to the peer.

No proxy, no port rewriting, no second process.

The DLL is loaded by replacing `ijl15.dll`: `game.dll` imports that file by
ordinal, so the shim runs before any LAN code. The original is kept as
`ijl15_orig.dll` and the shim forwards every export to it.

```
game.dll  --imports-->  ijl15.dll (shim)  --forwards-->  ijl15_orig.dll
                              |
                              +-- hooks WS2_32.sendto in game.dll
```

## Install

### Windows

1. Copy the game folder. `ijl15.dll`, `ijl15_orig.dll`, `wc3lan.peers` and
   `LEEME-LAN.txt` are already there.
2. Install Tailscale and log in to the same tailnet as the other players.
   The shim finds the peers by running `tailscale status --json`.
3. Open the game, go to **Local Area Network**.

Windows Firewall must allow `war3.exe` (UDP and TCP 6112) for the host.

### Linux (Lutris/Wine)

The shim works the same, but Wine cannot capture the output of the Linux
`tailscale` binary, so the peer list has to be written by hand:

```bash
./shim/install.sh "/path/to/WarCraft III"
printf '100.64.0.20\n100.64.0.30\n' > "/path/to/WarCraft III/wc3lan.peers"
```

Edit `wc3lan.peers` whenever the addresses change (one IPv4 per line, `#`
comments allowed). With ZeroTier no list is needed: its broadcast is forwarded
inside the network.

## Uninstall

```bash
./shim/uninstall.sh "/path/to/WarCraft III"
```

or copy `ijl15_orig.dll` over `ijl15.dll`.

## Troubleshooting

The shim writes `wc3lan.log` next to the game. A healthy start looks like:

```
shim loaded: 11 peers
hooked 1 sendto slot(s) in game.dll at 79020000
peers: 11
sendto socket bound to 0.0.0.0:6112
```

- `could not hook game.dll`: the import was not found (wrong build).
- `sendto socket bound to 192.168.x.x:6112`: the socket is bound to a single
  address, so unicast replies cannot come back. It should be `0.0.0.0`.
- No `game.dll` line: the shim was not loaded; check that `ijl15.dll` and
  `ijl15_orig.dll` are both in the game folder.

## Build

Requires Go (for the CLI) and `i686-w64-mingw32-gcc` (for the DLL).

```bash
make            # CLI + shim
make test
```

## The other tools

`cmd/wc3lan` is a Go CLI used during the investigation. It is not needed to
play, but it is useful:

| command | purpose |
|---|---|
| `wc3lan probe --host IP` | ask a host for its game (protocol check) |
| `wc3lan bridge --host IP` | republish a remote game on the local LAN (unpatched clients) |
| `wc3lan gateway` | serve a local game to remote clients |
| `wc3lan sim-host` / `sim-client` | fake host/client for tests |
| `wc3lan inspect game.dll` | show the patch site |
| `wc3lan patch game.dll --host IP` | alternative: patch the broadcast address to a fixed unicast IP (not universal) |
| `wc3lan unpatch game.dll` | restore from the `.wc3lan.bak` backup |

The `patch` command is the old approach. It works, but it bakes one IP into the
binary, so the shim is the recommended fix.

## Reverse engineering notes

See [docs/rea-findings.md](docs/rea-findings.md): the LAN module lives in
`game.dll` (not `war3.exe`), the five packet builders, `MakeBroadcastAddr` at
`0x6F0A25C0`, and how the shim's hook works.

## Legal

No game files are included. The shim only uses the executable already
installed by the player.
