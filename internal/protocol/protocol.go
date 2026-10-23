// Package protocol implements the Warcraft III classic (1.x) LAN protocol.
//
// Wire format
//
//	+--------+--------+------------------+
//	| magic  | opcode| length (uint16 LE)|
//	| 0xF7   | 1 byte| includes header  |
//	+--------+--------+------------------+
//
// LAN discovery runs over UDP port 6112 (broadcast), the actual multiplayer
// session runs over TCP (the port is advertised in the 0x30 "game info"
// packet, last two bytes).
//
// Reference: docs/protocol.md
package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

const (
	// MagicByte prefixes every LAN UDP and game TCP packet.
	MagicByte byte = 0xF7
	// HeaderSize is the size of the common 4 byte header.
	HeaderSize = 4
	// DefaultPort is the UDP discovery port and the default game TCP port.
	DefaultPort = 6112
)

// LAN discovery opcodes (UDP).
const (
	// OpQueryForLanGame is broadcast by a client to ask for available games.
	OpQueryForLanGame byte = 0x2F
	// OpGameInfo carries the full description of a hosted game.
	OpGameInfo byte = 0x30
	// OpGameAnnounce is broadcast when a game is created.
	OpGameAnnounce byte = 0x31
	// OpGamePlayersChanged is broadcast when the player count changes.
	OpGamePlayersChanged byte = 0x32
	// OpGameCancelled is broadcast when the game is closed.
	OpGameCancelled byte = 0x33
)

// GameType identifies the product that owns a game.
type GameType string

const (
	// GameTypeTFT is The Frozen Throne.
	GameTypeTFT GameType = "W3XP"
	// GameTypeROC is Reign of Chaos.
	GameTypeROC GameType = "WAR3"
)

// ErrShortPacket is returned when a packet is too small to be valid.
var ErrShortPacket = errors.New("protocol: packet too short")

// GameType names.
func (g GameType) String() string { return string(g) }

// ParseGameType accepts a game type string ("W3XP", "WAR3", case insensitive).
func ParseGameType(s string) (GameType, error) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "W3XP", "TFT", "PX3W":
		return GameTypeTFT, nil
	case "WAR3", "ROC", "3RAW":
		return GameTypeROC, nil
	default:
		return "", fmt.Errorf("protocol: unknown game type %q", s)
	}
}

// wireGameType returns the four bytes used on the wire. The protocol stores
// the game type reversed relative to the readable ASCII name.
func wireGameType(t GameType) []byte {
	switch t {
	case GameTypeTFT:
		return []byte{'P', 'X', '3', 'W'}
	case GameTypeROC:
		return []byte{'3', 'R', 'A', 'W'}
	default:
		return nil
	}
}

// parseGameType decodes a wire game type. Both byte orders are accepted so
// that different game builds can be handled without guessing.
func parseGameType(b []byte) (GameType, bool) {
	switch string(b) {
	case "PX3W", "W3XP":
		return GameTypeTFT, true
	case "3RAW", "WAR3":
		return GameTypeROC, true
	}
	return "", false
}

// Header is the common 4 byte packet header.
type Header struct {
	Op     byte
	Length uint16
}

// ParseHeader validates the magic byte and reads the header fields.
func ParseHeader(b []byte) (Header, error) {
	if len(b) < HeaderSize {
		return Header{}, ErrShortPacket
	}
	if b[0] != MagicByte {
		return Header{}, fmt.Errorf("protocol: bad magic byte 0x%02X", b[0])
	}
	return Header{Op: b[1], Length: binary.LittleEndian.Uint16(b[2:4])}, nil
}

// GameInfo is the decoded content of a 0x30 packet.
type GameInfo struct {
	GameType    GameType
	GameID      uint32
	TickCount   uint32
	Name        string
	Slots       uint32
	Flags       uint32
	Players     uint32
	PlayerSlots uint32
	Port        uint16

	// Decoded settings block.
	MapWidth    uint16
	MapHeight   uint16
	MapChecksum uint32
	MapName     string
	HostName    string
	MapSHA1     [20]byte
}

// DisplayPlayers is the number of human players shown in the lobby.
func (g GameInfo) DisplayPlayers() uint32 {
	return g.Players + (g.Slots - g.PlayerSlots)
}

// BuildQuery builds a 0x2F "query for LAN game" packet.
//
// The game id is always zero for a broadcast query.
func BuildQuery(t GameType, version uint32) ([]byte, error) {
	gt := wireGameType(t)
	if gt == nil {
		return nil, fmt.Errorf("protocol: unknown game type %q", t)
	}
	p := make([]byte, 16)
	p[0] = MagicByte
	p[1] = OpQueryForLanGame
	binary.LittleEndian.PutUint16(p[2:4], 16)
	copy(p[4:8], gt)
	binary.LittleEndian.PutUint32(p[8:12], version)
	// p[12:16] game id stays zero.
	return p, nil
}

// BuildGameAnnounce builds a 0x31 packet.
func BuildGameAnnounce(t GameType, version, gameID uint32) ([]byte, error) {
	gt := wireGameType(t)
	if gt == nil {
		return nil, fmt.Errorf("protocol: unknown game type %q", t)
	}
	p := make([]byte, 16)
	p[0] = MagicByte
	p[1] = OpGameAnnounce
	binary.LittleEndian.PutUint16(p[2:4], 16)
	copy(p[4:8], gt)
	binary.LittleEndian.PutUint32(p[8:12], version)
	binary.LittleEndian.PutUint32(p[12:16], gameID)
	return p, nil
}

// BuildPlayersChanged builds a 0x32 packet.
func BuildPlayersChanged(gameID, displayedPlayers, totalSlots uint32) []byte {
	p := make([]byte, 16)
	p[0] = MagicByte
	p[1] = OpGamePlayersChanged
	binary.LittleEndian.PutUint16(p[2:4], 16)
	binary.LittleEndian.PutUint32(p[4:8], gameID)
	binary.LittleEndian.PutUint32(p[8:12], displayedPlayers)
	binary.LittleEndian.PutUint32(p[12:16], totalSlots)
	return p
}

// BuildGameCancelled builds a 0x33 packet.
func BuildGameCancelled(gameID uint32) []byte {
	p := make([]byte, 8)
	p[0] = MagicByte
	p[1] = OpGameCancelled
	binary.LittleEndian.PutUint16(p[2:4], 8)
	binary.LittleEndian.PutUint32(p[4:8], gameID)
	return p
}

// GameInfoLayout describes where the fixed fields live inside a 0x30 packet.
const (
	gameInfoNameOffset   = 20
	gameInfoFooterLength = 22
)

// ParseGameInfo decodes a 0x30 packet.
//
// Layout (offsets from the start of the packet):
//
//	 0  magic
//	 1  opcode 0x30
//	 2  length (uint16 LE)
//	 4  game type (4 bytes)
//	 8  game id (uint32 LE)
//	12  tick count (uint32 LE)
//	16  unknown (4 bytes)
//	20  game name, NUL terminated
//	   encoded settings blob (see DecodeEncodedString)
//	-22 slots (uint32 LE)
//	-18 flags (uint32 LE)
//	-14 players (uint32 LE)
//	-10 player slots (uint32 LE)
//	 -6 unknown (uint32 LE)
//	 -2 game TCP port (uint16 LE)
func ParseGameInfo(p []byte) (*GameInfo, error) {
	h, err := ParseHeader(p)
	if err != nil {
		return nil, err
	}
	if h.Op != OpGameInfo {
		return nil, fmt.Errorf("protocol: expected opcode 0x%02X, got 0x%02X", OpGameInfo, h.Op)
	}
	if len(p) < gameInfoNameOffset+gameInfoFooterLength {
		return nil, ErrShortPacket
	}

	gt, ok := parseGameType(p[4:8])
	if !ok {
		return nil, fmt.Errorf("protocol: unknown game type %q", p[4:8])
	}

	g := &GameInfo{
		GameType:    gt,
		GameID:      binary.LittleEndian.Uint32(p[8:12]),
		TickCount:   binary.LittleEndian.Uint32(p[12:16]),
		Slots:       binary.LittleEndian.Uint32(p[len(p)-22:]),
		Flags:       binary.LittleEndian.Uint32(p[len(p)-18:]),
		Players:     binary.LittleEndian.Uint32(p[len(p)-14:]),
		PlayerSlots: binary.LittleEndian.Uint32(p[len(p)-10:]),
		Port:        binary.LittleEndian.Uint16(p[len(p)-2:]),
	}

	// The game name is a plain NUL terminated string.
	nameStart := gameInfoNameOffset
	nameEnd := -1
	for i := nameStart; i < len(p)-gameInfoFooterLength; i++ {
		if p[i] == 0 {
			nameEnd = i
			break
		}
	}
	if nameEnd < 0 {
		return nil, errors.New("protocol: game name is not terminated")
	}
	g.Name = string(p[nameStart:nameEnd])

	// The settings blob sits right after the name. Its exact start is not
	// documented, so try the plausible offsets and keep the one that decodes
	// cleanly and ends exactly at the footer.
	blobEnd := len(p) - gameInfoFooterLength
	var decoded []byte
	for skip := 1; skip <= 4; skip++ {
		start := nameEnd + skip
		if start >= blobEnd {
			break
		}
		d, consumed, err := DecodeEncodedString(p[start:blobEnd])
		if err != nil {
			continue
		}
		if consumed != blobEnd-start {
			continue
		}
		decoded = d
		break
	}
	if decoded == nil {
		return nil, errors.New("protocol: could not decode the settings blob")
	}
	applyDecodedSettings(g, decoded)
	return g, nil
}

// applyDecodedSettings fills the map related fields from the decoded blob.
//
// Layout of the decoded blob:
//
//	 0  game settings (uint32)
//	 4  zero
//	 5  map width (uint16)
//	 7  map height (uint16)
//	 9  map native checksum (uint32)
//	13  map name, NUL terminated
//	   host user name, NUL terminated
//	   zero
//	   20 bytes map SHA-1
func applyDecodedSettings(g *GameInfo, d []byte) {
	if len(d) < 13 {
		return
	}
	g.MapWidth = binary.LittleEndian.Uint16(d[5:7])
	g.MapHeight = binary.LittleEndian.Uint16(d[7:9])
	g.MapChecksum = binary.LittleEndian.Uint32(d[9:13])

	rest := d[13:]
	mapName, n, _ := readCString(rest)
	g.MapName = mapName
	rest = rest[n:]
	hostName, n2, _ := readCString(rest)
	g.HostName = hostName
	rest = rest[n2:]
	if len(rest) >= 20 {
		copy(g.MapSHA1[:], rest[:20])
	}
}

// readCString reads a NUL terminated string and returns it with the number of
// bytes consumed (including the terminator).
func readCString(b []byte) (string, int, error) {
	for i := 0; i < len(b); i++ {
		if b[i] == 0 {
			return string(b[:i]), i + 1, nil
		}
	}
	return "", 0, errors.New("protocol: unterminated string")
}

// DecodeEncodedString decodes the obfuscated settings blob.
//
// Every eighth byte is a control byte holding one bit per following data
// byte. When the bit is zero the data byte has to be decremented by one,
// otherwise it is used as is. A NUL byte terminates the blob.
//
// It returns the decoded bytes and how many input bytes were consumed.
func DecodeEncodedString(b []byte) ([]byte, int, error) {
	out := make([]byte, 0, len(b))
	var mask byte
	for i := 0; i < len(b); i++ {
		c := b[i]
		if c == 0 {
			return out, i + 1, nil
		}
		if i%8 == 0 {
			mask = c
			continue
		}
		if mask&(1<<(i%8)) == 0 {
			out = append(out, c-1)
		} else {
			out = append(out, c)
		}
	}
	return out, len(b), errors.New("protocol: encoded string is not terminated")
}

// EncodeEncodedString is the inverse of DecodeEncodedString. It is used to
// build synthetic packets for the local simulator and the tests.
//
// The control byte of every group holds bit 0 (always set) plus one bit per
// data byte. Bit k+1 belongs to the k-th data byte of the group: when it is
// set the byte is stored unchanged, otherwise the byte is stored incremented
// by one and the decoder has to decrement it again.
func EncodeEncodedString(plain []byte) []byte {
	out := make([]byte, 0, len(plain)+len(plain)/7+2)
	group := make([]byte, 0, 7)
	mask := byte(1)

	flush := func() {
		if len(group) == 0 {
			return
		}
		out = append(out, mask)
		out = append(out, group...)
		group = group[:0]
		mask = 1
	}

	for _, p := range plain {
		if p&1 == 1 {
			// Odd bytes are stored unchanged and their bit is set.
			group = append(group, p)
			mask |= 1 << len(group)
		} else {
			// Even bytes are stored incremented by one.
			group = append(group, p+1)
		}
		if len(group) == 7 {
			flush()
		}
	}
	flush()
	return append(out, 0)
}

// SetGameInfoPort overwrites the advertised TCP port of a 0x30 packet.
func SetGameInfoPort(p []byte, port uint16) {
	if len(p) < 2 {
		return
	}
	binary.LittleEndian.PutUint16(p[len(p)-2:], port)
}

// SetGameInfoName overwrites the game name of a 0x30 packet without changing
// the packet length. The replacement is padded with spaces; if it does not fit
// the original name is kept.
func SetGameInfoName(p []byte, name string) bool {
	if len(p) < gameInfoNameOffset {
		return false
	}
	end := -1
	for i := gameInfoNameOffset; i < len(p)-gameInfoFooterLength; i++ {
		if p[i] == 0 {
			end = i
			break
		}
	}
	if end < 0 {
		return false
	}
	orig := p[gameInfoNameOffset:end]
	if len(name) > len(orig) {
		return false
	}
	copy(orig, name)
	for i := len(name); i < len(orig); i++ {
		orig[i] = ' '
	}
	return true
}
