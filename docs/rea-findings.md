# REA findings: Warcraft III 1.27b LAN discovery

Target: `game.dll` from the 1.27b install ("The Frozen Throne").

| field | value |
|---|---|
| SHA-256 | `d51e5680243fc90e19c9d6074f7fac433c466d3cf5f46e2364291725574d8236` |
| image base | `0x6F000000` |
| `.text` | VA `0x6F001000`, file offset `0x400` |
| format | PE32, x86, WS2_32 imports by ordinal |

`war3.exe` only imports `WSOCK32.dll` for Battle.net; the LAN code lives in
`game.dll`. `Warcraft III.exe` is a 274 KB stub (`WARCRAFT_III_LAUNCHER`) and
`Frozen Throne.exe` is the same stub for the expansion, so neither is a patch
target.

## The one place broadcast is built

`MakeBroadcastAddr` at **VA `0x6F0A25C0`**:

```
0x6F0A25C0  push esi
0x6F0A25C1  mov  esi, edx              ; esi = sockaddr_in* out
0x6F0A25C3  mov  eax, 2
0x6F0A25C8  push ecx                    ; ecx = port, host order
0x6F0A25C9  mov  [esi], ax              ; sin_family = AF_INET
0x6F0A25CC  call [WS2_32_htons]
0x6F0A25D2  mov  [esi+2], ax            ; sin_port = htons(port)
0x6F0A25D6  xorps xmm0, xmm0
0x6F0A25D9  mov  dword [esi+4], -1       ; sin_addr = 255.255.255.255
0x6F0A25E0  movq [esi+8], xmm0          ; sin_zero
0x6F0A25E6  ret
```

The four address bytes live at **VA `0x6F0A25DC`**, file offset `0xA19DC`.

## Who calls it

Five packet builders in the LAN module, each one falling back to
`MakeBroadcastAddr(6112)` when the caller passes no explicit target:

| function | opcode | packet |
|---|---|---|
| `0x6F973100` | `0x30` | game info |
| `0x6F9732E0` | `0x31` | game announce |
| `0x6F9734C0` | `0x33` | game cancelled |
| `0x6F9736A0` | `0x2F` | query for LAN game |
| `0x6F973880` | `0x32` | players changed |

A query builder looks like this (`0x6F9736A0`):

```
push 0xF7                 ; magic byte, appended to the packet
push 0x2F                 ; opcode, appended to the packet
...
test ebx, ebx
jne  have_target
mov  ecx, 6112
call MakeBroadcastAddr   ; only when no target address was supplied
have_target:
push ebx                 ; sockaddr_in*
push 1                   ; count
push len
call send                ; virtual, ends in WS2_32 sendto
```

Because every LAN packet funnels through that single constructor, replacing
the four bytes at `0x6F0A25DC` redirects the discovery query and all the
announcements of a build to one unicast address.

## Byte order

`sin_addr` is a Winsock field, so it is network byte order: the file has to
contain the dotted quad in its natural order. Patching to `192.168.1.59`
writes `C0 A8 01 3B`. This is consistent with the sibling constructors:

- `0x6F0A2590` copies a caller supplied `sin_addr` dword,
- `0x6F0A25F0` builds an address from an `"ip:port"` string and stores the
  result of `WS2_32.inet_addr` directly into `sin_addr`,
- both set `sin_port` with `htons`.

## Reproduce

```
wc3lan inspect "/path/to/WarCraft III/game.dll"
wc3lan patch   "/path/to/WarCraft III/game.dll" --host 192.168.1.59
wc3lan unpatch "/path/to/WarCraft III/game.dll"
```

The patch key is `warcraft3-1.27b`. Any other build is rejected by the
`Expect` guard, which requires `FF FF FF FF` at the patch site.
