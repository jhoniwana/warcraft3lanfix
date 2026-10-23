package patch

import (
	"fmt"
	"sort"
	"strings"
)

// specs holds the patch definitions, one per confirmed game build.
//
// The entries are filled from the reverse engineering results of the real
// executables; see docs/rea-findings.md. Every site is located by a byte
// pattern so a single definition also covers small build differences.
var specs = map[string]Spec{}

func init() {
	// Warcraft III: The Frozen Throne 1.27b, game.dll (SHA-256
	// d51e5680243fc90e19c9d6074f7fac433c466d3cf5f46e2364291725574d8236).
	//
	// 0x6F0A25C0 builds the sockaddr_in every LAN packet is sent to:
	//
	//	mov [esi], 2                     ; sin_family = AF_INET
	//	call htons
	//	mov [esi+2], ax                  ; sin_port
	//	mov [esi+4], 0xFFFFFFFF          ; sin_addr = 255.255.255.255
	//
	// The five routines that build the LAN packets call it whenever no target
	// address was given:
	//
	//	0x6F973100  0x30 game info
	//	0x6F9732E0  0x31 game announce
	//	0x6F9734C0  0x33 game cancelled
	//	0x6F9736A0  0x2F query for LAN game
	//	0x6F973880  0x32 players changed
	//
	// Replacing the address in that single constructor redirects both the
	// discovery query and the announcements to one unicast address.
	Register("warcraft3-1.27b", Spec{
		Version: "1.27b (game.dll)",
		Notes: "LAN discovery broadcast -> unicast. " +
			"Patch site 0x6F0A25DC, the sin_addr of MakeBroadcastAddr.",
		Sites: []Site{{
			Name: "lan.broadcast_addr",
			Pattern: "B8 02 00 00 00 51 66 89 06 FF 15 ?? ?? ?? ?? " +
				"66 89 46 02 0F 57 C0 C7 46 04 FF FF FF FF",
			PatchOffset: 25,
			Expect:      "FF FF FF FF",
		}},
	})
}

// Register adds or replaces a patch definition.
func Register(name string, spec Spec) {
	specs[name] = spec
}

// SpecNames lists the known patch definitions.
func SpecNames() []string {
	out := make([]string, 0, len(specs))
	for name := range specs {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// SelectSpec returns the patch definition for an executable.
//
// When name is empty the definitions are tried in order and the one with the
// most matching sites wins. This keeps the tool usable on builds that were
// never analysed, as long as they share the patched code.
func SelectSpec(path, name string) (Spec, error) {
	if name != "" {
		spec, ok := specs[name]
		if !ok {
			return Spec{}, fmt.Errorf("patch: unknown patch definition %q, known: %s",
				name, strings.Join(SpecNames(), ", "))
		}
		return spec, nil
	}
	if len(specs) == 0 {
		return Spec{}, fmt.Errorf("patch: no patch definition is configured yet, the reverse engineering of the client is still pending")
	}

	best := Spec{}
	bestCount := 0
	for _, spec := range specs {
		matches, err := Inspect(path, spec)
		if err != nil || len(matches) == 0 {
			continue
		}
		if len(matches) > bestCount {
			best = spec
			bestCount = len(matches)
		}
	}
	if bestCount == 0 {
		return Spec{}, fmt.Errorf("patch: no known patch site was found in %s", path)
	}
	return best, nil
}
