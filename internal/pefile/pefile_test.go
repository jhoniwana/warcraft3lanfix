package pefile

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestMustPattern(t *testing.T) {
	p := MustPattern("FF FF .. 2F")
	if len(p.Bytes) != 4 {
		t.Fatalf("len = %d, want 4", len(p.Bytes))
	}
	if p.Mask[2] != 0 {
		t.Fatalf("the wildcard was not marked")
	}
	if p.Bytes[0] != 0xFF || p.Bytes[3] != 0x2F {
		t.Fatalf("the concrete bytes are wrong: % x", p.Bytes)
	}
}

func TestPatternMatch(t *testing.T) {
	p := MustPattern("FF FF .. 2F")
	data := []byte{0x00, 0xFF, 0xFF, 0x12, 0x2F}
	if !p.Match(data, 1) {
		t.Fatalf("the pattern should match at offset 1")
	}
	if p.Match(data, 0) {
		t.Fatalf("the pattern should not match at offset 0")
	}
	if p.Match(data, 2) {
		t.Fatalf("the pattern should not match at offset 2")
	}
	if p.Match(data, 3) {
		t.Fatalf("the pattern should not match past the end")
	}
}

func TestFindAllIn(t *testing.T) {
	data := []byte{0x11, 0xFF, 0xFF, 0x22, 0x2F, 0xFF, 0xFF, 0x33, 0x2F}
	offs := FindAllIn(data, MustPattern("FF FF .. 2F"))
	if len(offs) != 2 {
		t.Fatalf("found %d matches, want 2", len(offs))
	}
	if offs[0] != 1 || offs[1] != 5 {
		t.Fatalf("offsets = %v, want [1 5]", offs)
	}
}

func TestFindUint32(t *testing.T) {
	data := make([]byte, 32)
	binary.LittleEndian.PutUint32(data[4:8], 0xFFFFFFFF)
	p := Pattern{Bytes: []byte{0xFF, 0xFF, 0xFF, 0xFF}, Mask: []byte{1, 1, 1, 1}}
	offs := FindAllIn(data, p)
	if len(offs) != 1 || offs[0] != 4 {
		t.Fatalf("offsets = %v, want [4]", offs)
	}
}

func TestFindAllInNoMatch(t *testing.T) {
	data := bytes.Repeat([]byte{0x00}, 16)
	if offs := FindAllIn(data, MustPattern("FF FF FF FF")); len(offs) != 0 {
		t.Fatalf("found %d matches, want none", len(offs))
	}
}
