package protocol

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestBuildQuery(t *testing.T) {
	p, err := BuildQuery(GameTypeTFT, 27)
	if err != nil {
		t.Fatalf("BuildQuery: %v", err)
	}
	if len(p) != 16 {
		t.Fatalf("length = %d, want 16", len(p))
	}
	if p[0] != MagicByte || p[1] != OpQueryForLanGame {
		t.Fatalf("header = % x", p[:4])
	}
	if got := binary.LittleEndian.Uint16(p[2:4]); got != 16 {
		t.Fatalf("length field = %d, want 16", got)
	}
	if string(p[4:8]) != "PX3W" {
		t.Fatalf("game type = %q, want PX3W", p[4:8])
	}
	if got := binary.LittleEndian.Uint32(p[8:12]); got != 27 {
		t.Fatalf("version = %d, want 27", got)
	}
	if got := binary.LittleEndian.Uint32(p[12:16]); got != 0 {
		t.Fatalf("game id = %d, want 0", got)
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	plain := []byte("settings\x00\x05map name\x00host\x00")
	enc := EncodeEncodedString(plain)
	for _, b := range enc[:len(enc)-1] {
		if b == 0 {
			t.Fatal("encoded blob contains a NUL before the terminator")
		}
	}
	dec, consumed, err := DecodeEncodedString(enc)
	if err != nil {
		t.Fatalf("DecodeEncodedString: %v", err)
	}
	if consumed != len(enc) {
		t.Fatalf("consumed = %d, want %d", consumed, len(enc))
	}
	if !bytes.Equal(dec, plain) {
		t.Fatalf("round trip mismatch:\n got %q\nwant %q", dec, plain)
	}
}

func TestDecodeEncodedStringSpecExample(t *testing.T) {
	// From the packet specification: the control byte holds one bit per
	// following data byte, bit 1 belongs to the first byte. A zero bit means
	// the byte has to be decremented.
	//
	//   mask 0x03 -> the first byte is copied, the rest is decremented
	//   mask 0x01 -> every byte is decremented
	enc := []byte{0x03, 'A', 'B', 'C', 'D', 'E', 'F', 'G', 0x01, 'H', 0}
	dec, consumed, err := DecodeEncodedString(enc)
	if err != nil {
		t.Fatalf("DecodeEncodedString: %v", err)
	}
	if consumed != len(enc) {
		t.Fatalf("consumed = %d, want %d", consumed, len(enc))
	}
	want := string([]byte{'A'}) + string([]byte{'B' - 1, 'C' - 1, 'D' - 1, 'E' - 1, 'F' - 1, 'G' - 1}) + string([]byte{'H' - 1})
	if string(dec) != want {
		t.Fatalf("decoded = %q, want %q", dec, want)
	}

	// A mask with every data bit set copies the bytes unchanged.
	enc = []byte{0xFF, 'A', 'B', 'C', 'D', 'E', 'F', 'G', 0}
	dec, _, err = DecodeEncodedString(enc)
	if err != nil {
		t.Fatalf("DecodeEncodedString: %v", err)
	}
	if string(dec) != "ABCDEFG" {
		t.Fatalf("decoded = %q, want ABCDEFG", dec)
	}
}

// buildGameInfo assembles a 0x30 packet the way the game does.
func buildGameInfo(name, mapName, hostName string, port uint16, slots, players, playerSlots uint32) []byte {
	settings := make([]byte, 0, 64)
	settings = binary.LittleEndian.AppendUint32(settings, 0)
	settings = append(settings, 0)
	settings = binary.LittleEndian.AppendUint16(settings, 64) // width
	settings = binary.LittleEndian.AppendUint16(settings, 64) // height
	settings = binary.LittleEndian.AppendUint32(settings, 0xDEADBEEF)
	settings = append(settings, mapName...)
	settings = append(settings, 0)
	settings = append(settings, hostName...)
	settings = append(settings, 0)
	settings = append(settings, 0)
	settings = append(settings, make([]byte, 20)...)
	blob := EncodeEncodedString(settings)

	p := make([]byte, 0, 128)
	p = append(p, MagicByte, OpGameInfo, 0, 0)
	p = append(p, 'P', 'X', '3', 'W')
	p = binary.LittleEndian.AppendUint32(p, 7)     // game id
	p = binary.LittleEndian.AppendUint32(p, 12345) // tick count
	p = binary.LittleEndian.AppendUint32(p, 0)     // unknown
	p = append(p, name...)
	p = append(p, 0)
	p = append(p, blob...)
	p = binary.LittleEndian.AppendUint32(p, slots)
	p = binary.LittleEndian.AppendUint32(p, 0x09)
	p = binary.LittleEndian.AppendUint32(p, players)
	p = binary.LittleEndian.AppendUint32(p, playerSlots)
	p = binary.LittleEndian.AppendUint32(p, 0)
	p = binary.LittleEndian.AppendUint16(p, port)
	binary.LittleEndian.PutUint16(p[2:4], uint16(len(p)))
	return p
}

func TestParseGameInfo(t *testing.T) {
	p := buildGameInfo("Local Game", "Maps\\(2)\\Map01.w3m", "HostPlayer", 6112, 12, 2, 4)
	g, err := ParseGameInfo(p)
	if err != nil {
		t.Fatalf("ParseGameInfo: %v", err)
	}
	if g.GameType != GameTypeTFT {
		t.Errorf("game type = %v", g.GameType)
	}
	if g.GameID != 7 {
		t.Errorf("game id = %d", g.GameID)
	}
	if g.Name != "Local Game" {
		t.Errorf("name = %q", g.Name)
	}
	if g.MapName != "Maps\\(2)\\Map01.w3m" {
		t.Errorf("map name = %q", g.MapName)
	}
	if g.HostName != "HostPlayer" {
		t.Errorf("host name = %q", g.HostName)
	}
	if g.MapWidth != 64 || g.MapHeight != 64 {
		t.Errorf("map size = %dx%d", g.MapWidth, g.MapHeight)
	}
	if g.MapChecksum != 0xDEADBEEF {
		t.Errorf("checksum = %08X", g.MapChecksum)
	}
	if g.Port != 6112 {
		t.Errorf("port = %d", g.Port)
	}
	if g.Slots != 12 || g.Players != 2 || g.PlayerSlots != 4 {
		t.Errorf("slots=%d players=%d playerSlots=%d", g.Slots, g.Players, g.PlayerSlots)
	}
	if got := g.DisplayPlayers(); got != 10 {
		t.Errorf("display players = %d, want 10", got)
	}
}

func TestSetGameInfoPortKeepsLength(t *testing.T) {
	p := buildGameInfo("Local Game", "m", "h", 6112, 12, 2, 4)
	before := len(p)
	SetGameInfoPort(p, 6113)
	if len(p) != before {
		t.Fatalf("length changed")
	}
	g, err := ParseGameInfo(p)
	if err != nil {
		t.Fatalf("ParseGameInfo: %v", err)
	}
	if g.Port != 6113 {
		t.Fatalf("port = %d, want 6113", g.Port)
	}
}

func TestSetGameInfoName(t *testing.T) {
	p := buildGameInfo("Local Game", "m", "h", 6112, 12, 2, 4)
	if !SetGameInfoName(p, "Proxy Game") {
		t.Fatal("SetGameInfoName failed")
	}
	g, err := ParseGameInfo(p)
	if err != nil {
		t.Fatalf("ParseGameInfo: %v", err)
	}
	if g.Name != "Proxy Game" {
		t.Fatalf("name = %q", g.Name)
	}
	// A name that does not fit must be rejected instead of corrupting the packet.
	p2 := buildGameInfo("Local Game", "m", "h", 6112, 12, 2, 4)
	if SetGameInfoName(p2, "A very long replacement name") {
		t.Fatal("expected the rename to be rejected")
	}
}

func TestParseGameInfoRejectsGarbage(t *testing.T) {
	if _, err := ParseGameInfo([]byte{0x00, 0x01, 0x02, 0x03}); err == nil {
		t.Fatal("expected an error for a bad magic byte")
	}
	if _, err := ParseGameInfo(nil); err == nil {
		t.Fatal("expected an error for an empty packet")
	}
}
