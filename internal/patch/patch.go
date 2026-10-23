// Package patch applies the LAN discovery patch to a Warcraft III classic
// executable.
//
// The patch changes the address the client sends its "query for LAN game"
// packet to. The original code sends it to the broadcast address
// (255.255.255.255), which never leaves the local subnet. Pointing it at a
// single unicast address makes the client query the host directly, which is
// what a Tailscale or port forwarded setup needs.
//
// Every patch site is located by a byte pattern instead of a fixed offset so
// the same tool works for 1.21 - 1.31 and for Reign of Chaos or The Frozen
// Throne.
package patch

import (
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"wc3lan/internal/pefile"
)

// Site describes one patchable instruction sequence.
type Site struct {
	// Name identifies the site in the report.
	Name string
	// Pattern is the hex template of the code to find. A "." is a wildcard
	// nibble, so each character is one byte.
	Pattern string
	// PatchOffset is the byte offset inside the matched sequence that holds
	// the destination address.
	PatchOffset int
	// Expect is the hex template the bytes at PatchOffset must currently
	// have. It protects against patching the wrong build.
	Expect string
}

// Spec is a set of sites for one game build.
type Spec struct {
	// Version identifies the build the sites were confirmed against.
	Version string
	// Notes documents what each site does.
	Notes string
	// Sites are tried in order; every site that matches is patched.
	Sites []Site
}

// Report describes what a patch run found and changed.
type Report struct {
	Path       string
	Version    string
	BackupPath string
	DryRun     bool
	Matches    []MatchReport
}

// MatchReport describes one matched site.
type MatchReport struct {
	Site        string
	FileOffset  uint64
	PatchOffset int
	VA          uint64
	OldBytes    []byte
	NewBytes    []byte
	Applied     bool
}

// ErrNoSiteFound is returned when no patch site matched the file.
var ErrNoSiteFound = fmt.Errorf("patch: no known patch site was found in this build")

// Find locates every matching site of the spec inside the file.
func Find(f *pefile.File, spec Spec) ([]MatchReport, error) {
	var out []MatchReport
	for _, site := range spec.Sites {
		pat := pefile.MustPattern(site.Pattern)
		for _, off := range f.FindAll(pat) {
			addr := off + uint64(site.PatchOffset)
			if addr+4 > uint64(len(f.Raw)) {
				continue
			}
			va, _ := f.Address(off)
			mr := MatchReport{
				Site:        site.Name,
				FileOffset:  off,
				PatchOffset: site.PatchOffset,
				VA:          va,
				OldBytes:    append([]byte(nil), f.Raw[addr:addr+4]...),
			}
			if site.Expect != "" {
				want := pefile.MustPattern(site.Expect)
				if !want.Match(f.Raw, int(off)+site.PatchOffset) {
					// The sequence exists but the patched value does not
					// look like the expected original.
					continue
				}
			}
			out = append(out, mr)
		}
	}
	if len(out) == 0 {
		return nil, ErrNoSiteFound
	}
	return out, nil
}

// Apply patches the file so that the LAN discovery query goes to ip.
//
// The original file is copied to "<name>.wc3lan.bak" before anything is
// written. When dryRun is true nothing is written.
func Apply(path string, ip net.IP, spec Spec, dryRun bool) (*Report, error) {
	v4 := ip.To4()
	if v4 == nil {
		return nil, fmt.Errorf("patch: %s is not an IPv4 address", ip)
	}

	f, err := pefile.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	matches, err := Find(f, spec)
	if err != nil {
		return nil, err
	}

	rep := &Report{
		Path:    path,
		Version: spec.Version,
		DryRun:  dryRun,
		Matches: matches,
	}

	// sin_addr holds the address in network byte order, so the four bytes
	// are the dotted quad in the order the user typed it.
	newAddr := sockaddrBytes(v4)

	backup := path + ".wc3lan.bak"
	if !dryRun {
		if err := backupFile(path, backup); err != nil {
			return nil, err
		}
		rep.BackupPath = backup
	}

	for i := range rep.Matches {
		m := &rep.Matches[i]
		m.NewBytes = newAddr
		if dryRun {
			continue
		}
		off := m.FileOffset + uint64(m.PatchOffset)
		if err := f.WriteFileOffset(off, newAddr); err != nil {
			return nil, err
		}
		m.Applied = true
	}

	if !dryRun {
		if err := os.WriteFile(path, f.Raw, 0o755); err != nil {
			return nil, err
		}
	}
	return rep, nil
}

// sockaddrBytes returns the four bytes of a sin_addr field for ip.
//
// A sockaddr_in keeps the address in network byte order, so the bytes are the
// dotted quad in its natural order: 192.168.1.10 is C0 A8 01 0A.
func sockaddrBytes(ip net.IP) []byte {
	v4 := ip.To4()
	if v4 == nil {
		return nil
	}
	return []byte{v4[0], v4[1], v4[2], v4[3]}
}

// backupFile copies src to dst without overwriting an existing backup.
func backupFile(src, dst string) error {
	if _, err := os.Stat(dst); err == nil {
		// Keep the first pristine backup, but refresh it when it is older
		// than the current file.
		srcInfo, err := os.Stat(src)
		if err != nil {
			return err
		}
		dstInfo, err := os.Stat(dst)
		if err != nil {
			return err
		}
		if dstInfo.ModTime().After(srcInfo.ModTime()) {
			return nil
		}
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o755)
}

// Restore copies the backup over the patched file.
func Restore(path string) (string, error) {
	backup := path + ".wc3lan.bak"
	data, err := os.ReadFile(backup)
	if err != nil {
		return "", fmt.Errorf("patch: cannot read the backup: %w", err)
	}
	if err := os.WriteFile(path, data, 0o755); err != nil {
		return "", err
	}
	return backup, nil
}

// Inspect reports the bytes that the patch would replace, without changing
// anything. It is used to confirm the analysis against a real build.
func Inspect(path string, spec Spec) ([]MatchReport, error) {
	f, err := pefile.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Find(f, spec)
}

// Describe renders a report for the command line.
func (r *Report) Describe() string {
	out := ""
	for _, m := range r.Matches {
		state := "dry-run"
		if m.Applied {
			state = "patched"
		}
		out += fmt.Sprintf("  %-28s file+0x%X  va=0x%X  %s -> %s  [%s]\n",
			m.Site, m.FileOffset+uint64(m.PatchOffset), m.VA+uint64(m.PatchOffset),
			fmt.Sprintf("%02X%02X%02X%02X", m.OldBytes[0], m.OldBytes[1], m.OldBytes[2], m.OldBytes[3]),
			fmt.Sprintf("%02X%02X%02X%02X", m.NewBytes[0], m.NewBytes[1], m.NewBytes[2], m.NewBytes[3]),
			state)
	}
	return out
}

// Stamp returns a short marker for the backup file name.
func Stamp() string {
	return time.Now().UTC().Format("20060102-150405")
}

// CleanPath removes the directory part of a path, used in log lines.
func CleanPath(p string) string {
	return filepath.Base(p)
}

// Uint32LE is a small helper used by the tests.
func Uint32LE(b []byte) uint32 { return binary.LittleEndian.Uint32(b) }
