package romloader

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
)

// Intel HEX record types we care about.
const (
	ihexData          = 0x00
	ihexEOF           = 0x01
	ihexExtSegAddr    = 0x02 // I8086 segment*16; not used by these dumps but accepted
	ihexStartSegAddr  = 0x03 // ignored
	ihexExtLinearAddr = 0x04 // upper 16 bits of 32-bit address; the format used here
	ihexStartLinAddr  = 0x05 // ignored
)

// ParseIntelHEX decodes an Intel-HEX stream into a flat byte buffer sized to
// the highest address written. Bytes not covered by any data record default to
// 0xFF (unprogrammed EEPROM). Returns the decoded image; unsupported record
// types are tolerated (segment-start / linear-start are ignored).
//
// Both Extended Linear Address (type 04) and Extended Segment Address (type
// 02) prefixes are honoured so the parser handles either flavour of HEX.
func ParseIntelHEX(r io.Reader) ([]byte, error) {
	const sentinel = 0xFF
	// Start with 64 KB and grow as records arrive — keeps the small case cheap.
	out := make([]byte, 0, 1<<16)

	var baseAddr uint32 // upper 16 bits set by type-04 (or type-02 × 16)
	br := bufio.NewScanner(r)
	br.Buffer(make([]byte, 0, 1<<14), 1<<20)
	for line := 1; br.Scan(); line++ {
		s := strings.TrimSpace(br.Text())
		if s == "" {
			continue
		}
		if !strings.HasPrefix(s, ":") {
			return nil, fmt.Errorf("ihex line %d: missing ':' start code", line)
		}
		raw, err := hex.DecodeString(s[1:])
		if err != nil {
			return nil, fmt.Errorf("ihex line %d: %w", line, err)
		}
		if len(raw) < 5 {
			return nil, fmt.Errorf("ihex line %d: short record (%d bytes)", line, len(raw))
		}
		n := int(raw[0])
		if len(raw) != 1+2+1+n+1 {
			return nil, fmt.Errorf("ihex line %d: length byte %d disagrees with record length %d",
				line, n, len(raw))
		}
		// Verify checksum: sum of all bytes (including checksum) mod 256 == 0.
		var sum byte
		for _, b := range raw {
			sum += b
		}
		if sum != 0 {
			return nil, fmt.Errorf("ihex line %d: checksum mismatch", line)
		}

		addr := uint32(raw[1])<<8 | uint32(raw[2])
		typ := raw[3]
		data := raw[4 : 4+n]

		switch typ {
		case ihexData:
			full := baseAddr + addr
			need := int(full) + n
			if need > len(out) {
				// Grow + pad with sentinel (0xFF — unprogrammed EEPROM).
				old := len(out)
				if need > cap(out) {
					grown := make([]byte, need)
					copy(grown, out)
					out = grown
				} else {
					out = out[:need]
				}
				for i := old; i < need; i++ {
					out[i] = sentinel
				}
			}
			copy(out[full:full+uint32(n)], data)
		case ihexEOF:
			// Some files have trailing whitespace records after EOF; tolerate them.
			return out, nil
		case ihexExtLinearAddr:
			if n != 2 {
				return nil, fmt.Errorf("ihex line %d: type-04 data length %d, want 2", line, n)
			}
			baseAddr = (uint32(data[0])<<8 | uint32(data[1])) << 16
		case ihexExtSegAddr:
			if n != 2 {
				return nil, fmt.Errorf("ihex line %d: type-02 data length %d, want 2", line, n)
			}
			baseAddr = (uint32(data[0])<<8 | uint32(data[1])) << 4
		case ihexStartSegAddr, ihexStartLinAddr:
			// Start-address records carry the initial CS:IP / EIP — irrelevant
			// for M68K firmware. Ignore.
		default:
			return nil, fmt.Errorf("ihex line %d: unsupported record type %#02x", line, typ)
		}
	}
	if err := br.Err(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("ihex: input ended without an EOF record")
}

// LoadHEXFile parses an Intel-HEX file from disk; small wrapper for callers
// that already have a path.
func LoadHEXFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ParseIntelHEX(f)
}
