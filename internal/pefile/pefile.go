// Package pefile provides the few PE facts the patcher needs: section layout,
// virtual address to file offset mapping and byte pattern search.
package pefile

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

// File is a parsed PE image kept in memory.
type File struct {
	Path      string
	Raw       []byte
	PE        *pe.File
	ImageBase uint64
}

// Section describes one mapped section.
type Section struct {
	Name      string
	VA        uint64 // virtual address (relative to the image base)
	VSize     uint64
	RawOffset uint64
	RawSize   uint64
	Flags     uint32
}

// Open reads and parses a PE file.
func Open(path string) (*File, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pf, err := pe.NewFile(strings.NewReader(string(raw)))
	if err != nil {
		return nil, fmt.Errorf("pefile: %w", err)
	}
	return &File{Path: path, Raw: raw, PE: pf, ImageBase: imageBase(pf)}, nil
}

// imageBase reads the image base from the optional header.
func imageBase(pf *pe.File) uint64 {
	switch oh := pf.OptionalHeader.(type) {
	case *pe.OptionalHeader64:
		return oh.ImageBase
	case *pe.OptionalHeader32:
		return uint64(oh.ImageBase)
	}
	return 0
}

// Close releases the parsed image.
func (f *File) Close() error { return f.PE.Close() }

// Sections returns the section table.
func (f *File) Sections() []Section {
	var out []Section
	for _, s := range f.PE.Sections {
		out = append(out, Section{
			Name:      s.Name,
			VA:        uint64(s.VirtualAddress),
			VSize:     uint64(s.VirtualSize),
			RawOffset: uint64(s.Offset),
			RawSize:   uint64(s.Size),
			Flags:     s.Characteristics,
		})
	}
	return out
}

// Section returns the section that contains the given relative virtual address.
func (f *File) Section(va uint64) (Section, bool) {
	for _, s := range f.Sections() {
		size := s.VSize
		if size == 0 {
			size = s.RawSize
		}
		if va >= s.VA && va < s.VA+size {
			return s, true
		}
	}
	return Section{}, false
}

// FileOffset converts a relative virtual address into a file offset.
// It returns false when the address is not backed by file data.
func (f *File) FileOffset(va uint64) (uint64, bool) {
	s, ok := f.Section(va)
	if !ok {
		return 0, false
	}
	delta := va - s.VA
	if delta >= s.RawSize {
		return 0, false
	}
	return s.RawOffset + delta, true
}

// Address converts a file offset back into a relative virtual address.
func (f *File) Address(offset uint64) (uint64, bool) {
	for _, s := range f.Sections() {
		if offset >= s.RawOffset && offset < s.RawOffset+s.RawSize {
			return s.VA + (offset - s.RawOffset), true
		}
	}
	return 0, false
}

// ReadVirtual returns the bytes of a relative virtual address.
func (f *File) ReadVirtual(va uint64, n int) ([]byte, error) {
	off, ok := f.FileOffset(va)
	if !ok {
		return nil, fmt.Errorf("pefile: virtual address 0x%X is not backed by file data", va)
	}
	if off+uint64(n) > uint64(len(f.Raw)) {
		return nil, fmt.Errorf("pefile: read past the end of the file")
	}
	return f.Raw[off : off+uint64(n)], nil
}

// WriteFileOffset overwrites bytes at a file offset and flushes the change.
func (f *File) WriteFileOffset(offset uint64, data []byte) error {
	if offset+uint64(len(data)) > uint64(len(f.Raw)) {
		return fmt.Errorf("pefile: write past the end of the file")
	}
	copy(f.Raw[offset:], data)
	return nil
}

// Pattern is a byte sequence with optional wildcards.
//
// A byte value of 0x00 in a wildcard position is not allowed, use the
// Wildcard constant instead.
type Pattern struct {
	Bytes []byte
	Mask  []byte
}

// Wildcard marks an unknown byte inside a pattern.
const Wildcard byte = 0x00

// MustPattern builds a pattern from a template string.
//
// Every byte is written as two hex digits. A run of dots or question marks is
// one wildcard byte. Whitespace separates the bytes but is optional:
//
//	"C7 46 04 FF FF FF FF"
//	"C74604FFFFFFFF"
//	"C7 46 04 ?? ?? ?? ??"
func MustPattern(tmpl string) Pattern {
	var p Pattern
	for i := 0; i < len(tmpl); {
		c := tmpl[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '.' || c == '?':
			for i < len(tmpl) && (tmpl[i] == '.' || tmpl[i] == '?') {
				i++
			}
			p.Bytes = append(p.Bytes, Wildcard)
			p.Mask = append(p.Mask, 0)
		case isHexDigit(c):
			if i+1 >= len(tmpl) || !isHexDigit(tmpl[i+1]) {
				panic(fmt.Sprintf("pefile: incomplete pattern byte at %q", tmpl[i:]))
			}
			v, err := strconv.ParseUint(tmpl[i:i+2], 16, 8)
			if err != nil {
				panic(fmt.Sprintf("pefile: invalid pattern byte %q: %v", tmpl[i:i+2], err))
			}
			p.Bytes = append(p.Bytes, byte(v))
			p.Mask = append(p.Mask, 1)
			i += 2
		default:
			panic(fmt.Sprintf("pefile: invalid pattern byte %q", c))
		}
	}
	return p
}

// isHexDigit reports whether c is a hexadecimal digit.
func isHexDigit(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

// Match reports a pattern match at the given offset.
func (p Pattern) Match(data []byte, off int) bool {
	if off < 0 || off+len(p.Bytes) > len(data) {
		return false
	}
	for i := range p.Bytes {
		if p.Mask[i] == 0 {
			continue
		}
		if data[off+i] != p.Bytes[i] {
			return false
		}
	}
	return true
}

// FindAll returns every file offset where the pattern matches.
func (f *File) FindAll(p Pattern) []uint64 {
	return FindAllIn(f.Raw, p)
}

// FindAllIn returns every offset of data where the pattern matches.
func FindAllIn(data []byte, p Pattern) []uint64 {
	var out []uint64
	for off := 0; off+len(p.Bytes) <= len(data); off++ {
		if p.Match(data, off) {
			out = append(out, uint64(off))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// FindUint32 searches for a little endian 32 bit value and returns the file
// offsets where it appears.
func (f *File) FindUint32(value uint32) []uint64 {
	var needle [4]byte
	binary.LittleEndian.PutUint32(needle[:], value)
	p := Pattern{Bytes: needle[:], Mask: []byte{1, 1, 1, 1}}
	return f.FindAll(p)
}

// FindUint16 searches for a little endian 16 bit value.
func (f *File) FindUint16(value uint16) []uint64 {
	var needle [2]byte
	binary.LittleEndian.PutUint16(needle[:], value)
	p := Pattern{Bytes: needle[:], Mask: []byte{1, 1}}
	return f.FindAll(p)
}

// ProductVersion returns the PE product version string, if present.
//
// The classic Warcraft III executables do not always carry a version
// resource, so an empty string simply means "unknown".
func (f *File) ProductVersion() string {
	res := f.PE.Section(".rsrc")
	if res == nil {
		return ""
	}
	data, err := res.Data()
	if err != nil {
		return ""
	}
	return findVersionString(data)
}

// findVersionString looks for the FileVersion string inside a resource
// section.
func findVersionString(data []byte) string {
	needle := utf16Bytes("VS_VERSION_INFO")
	idx := indexBytes(data, needle)
	if idx < 0 {
		return ""
	}
	// The VS_FIXEDFILEINFO structure follows the key. Its file version is
	// kept in the first two 32 bit values.
	ffi := idx + len(needle)
	if ffi+8 > len(data) {
		return ""
	}
	// Skip to the aligned structure start.
	for ffi%4 != 0 {
		ffi++
		if ffi+8 > len(data) {
			return ""
		}
	}
	// The fixed info starts with a signature of 0xFEEF04BD.
	if binary.LittleEndian.Uint32(data[ffi:ffi+4]) != 0xFEEF04BD {
		return ""
	}
	ms := binary.LittleEndian.Uint32(data[ffi+8 : ffi+12])
	ls := binary.LittleEndian.Uint32(data[ffi+12 : ffi+16])
	return fmt.Sprintf("%d.%d.%d.%d", ms>>16, ms&0xFFFF, ls>>16, ls&0xFFFF)
}

// utf16Bytes encodes a string as UTF-16LE bytes.
func utf16Bytes(s string) []byte {
	units := utf16.Encode([]rune(s))
	out := make([]byte, 0, len(units)*2)
	for _, u := range units {
		out = append(out, byte(u), byte(u>>8))
	}
	return out
}

// indexBytes returns the index of the first occurrence of needle.
func indexBytes(haystack, needle []byte) int {
	if len(needle) == 0 || len(haystack) < len(needle) {
		return -1
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if bytes.Equal(haystack[i:i+len(needle)], needle) {
			return i
		}
	}
	return -1
}
