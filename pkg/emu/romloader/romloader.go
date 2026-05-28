// Package romloader reconstructs the HP 8593A firmware ROM image from the four
// raw EEPROM dumps. It is the canonical source of truth; never load rom.bin
// directly in production or test code — derive the image through Build.
//
// Active firmware: Rev L 98.06.15 (Option 027 variant) — Y2K-compliant, supports
// the 8594L, fixes scaling errors. Four 27C020 EEPROMs (256 KB each = 1 MB
// total). Shipped as Intel HEX dumps; LoadDir parses them on the fly.
//
// The earlier 17.12.90 development build (four 27C010 = 512 KB) was archived
// under hp8593a_eeproms/legacy_17.12.90/ when Rev L became canonical; its raw
// .bin files can still be loaded by reading them directly and calling Build
// with the matching chip ordering — see legacy notes in that directory.
//
// Hardware layout (Rev L)
//
// The HP 8593A uses a 16-bit M68K data bus split across two byte-wide EEPROMs
// per bank. Rev L's bank ordering on the A16 PCB places the U24+U7 pair in the
// lower bank (offset 0, containing the reset vector) and U23+U6 in the upper
// bank. This is empirically derived from the reset vector — Rev L Opt-027
// produces SP=0x00FF948A, PC=0x00001B34 only with this bank arrangement (vs the
// 17.12.90 build, which had U23+U6 in the lower bank).
//
//	Lower bank (0x000000–0x07FFFF):  U24 (MSB) + U7  (LSB)
//	Upper bank (0x080000–0x0FFFFF):  U23 (MSB) + U6  (LSB)
//
// Within each bank every M68K word is formed by interleaving one MSB byte from
// the first chip and one LSB byte from the second chip:
//
//	rom[2i]   = msb[i]
//	rom[2i+1] = lsb[i]
//
// The two banks are then concatenated to form the 1 MB image.
package romloader

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// ChipSize is the size of each Rev L EEPROM in bytes (27C020 = 256 KB).
	ChipSize = 262144
	// ROMSize is the full interleaved image size (4 × ChipSize = 1 MB).
	ROMSize = ChipSize * 4
)

// Build constructs the 1 MB ROM image by interleaving the four EEPROM contents.
// Bytes from the MSB chip alternate with bytes from the LSB chip within each
// bank; the lower bank precedes the upper bank in the output.
//
//   - lowerMSB: U24 (lower bank MSB; reset vector lives here at offset 0)
//   - lowerLSB: U7  (lower bank LSB)
//   - upperMSB: U23 (upper bank MSB)
//   - upperLSB: U6  (upper bank LSB)
func Build(lowerMSB, lowerLSB, upperMSB, upperLSB []byte) ([]byte, error) {
	for i, b := range [][]byte{lowerMSB, lowerLSB, upperMSB, upperLSB} {
		if len(b) != ChipSize {
			return nil, fmt.Errorf("romloader: chip[%d] is %d bytes, want %d (%d KB)",
				i, len(b), ChipSize, ChipSize/1024)
		}
	}
	rom := make([]byte, ROMSize)
	joinBank(rom[0:], lowerMSB, lowerLSB)
	joinBank(rom[ChipSize*2:], upperMSB, upperLSB)
	return rom, nil
}

// joinBank interleaves msb[i] / lsb[i] pairs into dst starting at dst[0].
func joinBank(dst []byte, msb, lsb []byte) {
	for i := range lsb {
		dst[i*2] = msb[i]
		dst[i*2+1] = lsb[i]
	}
}

// ChipPaths holds the four canonical Rev L EEPROM file names relative to the
// hp8593a_eeproms/ directory. Order matches Build's argument list:
// lowerMSB, lowerLSB, upperMSB, upperLSB.
var ChipPaths = [4]string{
	"U24-98-06-15.HEX", // lower MSB
	"U7-98-06-15.HEX",  // lower LSB
	"U23-98-06-15.HEX", // upper MSB
	"U6-98-06-15.HEX",  // upper LSB
}

// LoadDir reads the four Rev L EEPROM dumps from dir (the hp8593a_eeproms
// directory) and returns the assembled 1 MB ROM image. Each chip dump is
// parsed as Intel HEX (the format the firmware kit ships in).
func LoadDir(dir string) ([]byte, error) {
	chips := [4][]byte{}
	for i, name := range ChipPaths {
		data, err := loadChip(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("romloader: %s: %w", name, err)
		}
		chips[i] = data
	}
	return Build(chips[0], chips[1], chips[2], chips[3])
}

// loadChip reads one EEPROM dump. .HEX files are parsed as Intel HEX; anything
// else is treated as a raw binary blob. The parser pads short data with 0xFF
// (unprogrammed EEPROM); we trim/pad to ChipSize so Build's invariant holds.
func loadChip(path string) ([]byte, error) {
	if strings.EqualFold(filepath.Ext(path), ".hex") {
		data, err := LoadHEXFile(path)
		if err != nil {
			return nil, err
		}
		return fitChip(data), nil
	}
	return os.ReadFile(path)
}

// fitChip extends a short HEX-decoded blob with 0xFF (unprogrammed EEPROM) to
// exactly ChipSize, or trims a longer one. Real Rev L HEX dumps fill every byte
// so neither branch fires in practice; the safety belt is for partial dumps.
func fitChip(data []byte) []byte {
	if len(data) == ChipSize {
		return data
	}
	out := make([]byte, ChipSize)
	for i := range out {
		out[i] = 0xFF
	}
	copy(out, data)
	return out
}
