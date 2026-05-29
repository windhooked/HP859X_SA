// Command jumptable extracts the firmware's master dispatch table at
// ROM 0xC4..0x738 (650 entries × 6 bytes = 4EF9 JMP absolute long),
// then walks the parser-name table at 0x07E780+ and maps each command
// to its slot + dispatch target.
//
// The handler-byte encoding for parser-table entries (e.g. for IDNUM:
// `00 96 01 B6`) decomposes as:
//   byte 0      — arg/type high byte (often 0)
//   byte 1      — arg low byte (passed to the slot's helper as d0)
//   bytes 2..3  — slot offset from table base 0xC4 (must be 6-aligned)
//
// Validates by checking that the computed slot PC contains 4EF9 (JMP)
// and prints the JMP target.
package main

import (
	"encoding/binary"
	"fmt"

	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

const (
	dispatchTableBase = 0x000C4
	dispatchTableEnd  = 0x01B34 // 1128 entries × 6 bytes = 0x1A70 from 0xC4
	parserTableStart  = 0x07C800
	parserTableEnd    = 0x080000
)

func main() {
	rom, err := romloader.LoadDir("hp8593a_eeproms")
	if err != nil {
		panic(err)
	}

	// First, extract the full dispatch table.
	fmt.Println("=== firmware master dispatch table at ROM 0xC4..0x738 ===")
	slots := make(map[uint32]uint32) // slot PC → JMP target
	for pc := uint32(dispatchTableBase); pc < dispatchTableEnd+6; pc += 6 {
		if rom[pc] == 0x4E && rom[pc+1] == 0xF9 {
			target := binary.BigEndian.Uint32(rom[pc+2 : pc+6])
			slots[pc] = target
		}
	}
	fmt.Printf("  %d slots extracted (each = 4EF9 + 4-byte target)\n\n", len(slots))

	// Walk the parser-name table looking for entries with the format:
	//   <tag-byte> <subtype> <NUL-terminated name> <handler bytes>
	// We focus on long mnemonics (3+ chars) that fit this pattern.
	type entry struct {
		off       uint32
		name      string
		handler   []byte
		slotPC    uint32
		jumpTarget uint32
		valid     bool
	}
	var entries []entry

	for off := uint32(parserTableStart); off < parserTableEnd-8; off++ {
		tag := rom[off]
		if tag != 0x30 && tag != 0x40 && tag != 0x10 && tag != 0x20 {
			continue
		}
		// Subtype byte at off+1, name starts at off+2.
		nameStart := off + 2
		nameEnd := nameStart
		for nameEnd < off+30 && nameEnd < uint32(len(rom)) && rom[nameEnd] >= 0x20 && rom[nameEnd] < 0x7F {
			nameEnd++
		}
		if nameEnd == nameStart {
			continue
		}
		// Look for NUL after name (parser-table style).
		if rom[nameEnd] != 0x00 {
			continue
		}
		name := string(rom[nameStart:nameEnd])
		// Trim trailing spaces.
		for len(name) > 0 && name[len(name)-1] == ' ' {
			name = name[:len(name)-1]
		}
		if len(name) < 2 || len(name) > 12 {
			continue
		}
		// Check all chars are alphanumeric (no special chars).
		ok := true
		for _, c := range []byte(name) {
			if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_') {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		// Handler bytes after NUL — read 4 bytes.
		hStart := nameEnd + 1
		if hStart+4 > uint32(len(rom)) {
			continue
		}
		handler := rom[hStart : hStart+4]
		// Decode: bytes 2-3 = slot offset from 0xC4.
		slotOffset := binary.BigEndian.Uint16(handler[2:4])
		slotPC := uint32(dispatchTableBase) + uint32(slotOffset)
		e := entry{
			off:     off,
			name:    name,
			handler: append([]byte{}, handler...),
		}
		if slotPC >= dispatchTableBase && slotPC < dispatchTableEnd+6 && (slotOffset%6 == 0) {
			if target, ok := slots[slotPC]; ok {
				e.slotPC = slotPC
				e.jumpTarget = target
				e.valid = true
			}
		}
		entries = append(entries, e)
		off = nameEnd + 4 // skip past handler bytes
	}

	// Dedupe by name.
	seen := make(map[string]bool)
	var unique []entry
	for _, e := range entries {
		if seen[e.name] {
			continue
		}
		seen[e.name] = true
		unique = append(unique, e)
	}

	fmt.Printf("=== parser-table command entries: %d unique ===\n\n", len(unique))
	fmt.Println("name (hex handler) → slot PC → JMP target")
	validCount := 0
	for _, e := range unique {
		hex := fmt.Sprintf("%02X %02X %02X %02X", e.handler[0], e.handler[1], e.handler[2], e.handler[3])
		if e.valid {
			fmt.Printf("  %-15s (%s)  slot 0x%04X → jmp 0x%06X\n",
				e.name, hex, e.slotPC, e.jumpTarget)
			validCount++
		} else {
			fmt.Printf("  %-15s (%s)  (non-slot encoding)\n", e.name, hex)
		}
	}

	fmt.Printf("\n=== summary ===\n")
	fmt.Printf("  %d unique parser commands\n", len(unique))
	fmt.Printf("  %d resolve to a valid dispatch-table slot\n", validCount)
	fmt.Printf("  %d use a different (non-slot) encoding\n", len(unique)-validCount)
}
