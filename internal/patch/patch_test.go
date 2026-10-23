package patch

import (
	"bytes"
	"net"
	"testing"

	"wc3lan/internal/pefile"
)

func TestFrozenThroneSpec(t *testing.T) {
	spec, err := SelectSpec("", "warcraft3-1.27b")
	if err != nil {
		t.Fatalf("SelectSpec: %v", err)
	}
	if len(spec.Sites) != 1 {
		t.Fatalf("sites = %d, want 1", len(spec.Sites))
	}
	site := spec.Sites[0]

	// The pattern has to end with the four address bytes so that PatchOffset
	// always points at the sin_addr field.
	pat := pefile.MustPattern(site.Pattern)
	if site.PatchOffset+4 != len(pat.Bytes) {
		t.Fatalf("PatchOffset %d does not point at the last 4 bytes of a %d byte pattern",
			site.PatchOffset, len(pat.Bytes))
	}
	if !bytes.Equal(pat.Bytes[site.PatchOffset:], []byte{0xFF, 0xFF, 0xFF, 0xFF}) {
		t.Fatalf("the pattern does not end with the broadcast address: % x", pat.Bytes[site.PatchOffset:])
	}
}

func TestSockaddrBytes(t *testing.T) {
	// sin_addr is stored in network byte order: the dotted quad appears in
	// the file in the order the user typed it.
	got := sockaddrBytes(net.ParseIP("192.168.1.10").To4())
	want := []byte{192, 168, 1, 10}
	if !bytes.Equal(got, want) {
		t.Fatalf("sockaddrBytes = % x, want % x", got, want)
	}
}
