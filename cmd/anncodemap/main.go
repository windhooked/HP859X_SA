// Command anncodemap statically maps every annunciator handler call site. It
// scans the ROM for `jsr (d16,PC)` (opcode 0x4EBA) targeting the annunciator
// add/remove functions (fcn.e7f0 add, fcn.e87e remove, fcn.e7a2 add-variant)
// and reads the `moveq #code,D0` (0x70nn) two bytes before — the annunciator
// code. Output: code -> {add sites, remove sites}, so OVEN COLD (0x0D) and
// ADC-TIME (0x28) handler locations + their status tests can be read off.
package main

import (
	"fmt"
	"sort"

	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

func main() {
	rom, _ := romloader.LoadDir("hp8593a_eeproms")
	targets := map[uint32]string{0xE7F0: "ADD", 0xE87E: "REMOVE", 0xE7A2: "ADD2"}
	be16 := func(a uint32) uint16 { return uint16(rom[a])<<8 | uint16(rom[a+1]) }
	type site struct {
		addr uint32
		kind string
	}
	byCode := map[uint8][]site{}
	for a := uint32(0x400); a < 0x100000-4; a += 2 {
		if be16(a) != 0x4EBA { // jsr (d16,PC)
			continue
		}
		off := int16(be16(a + 2))
		target := uint32(int64(a) + 2 + int64(off))
		kind, ok := targets[target]
		if !ok {
			continue
		}
		// preceding instruction at a-2 should be moveq #code,D0 = 0x70nn
		if a >= 2 && rom[a-2] == 0x70 {
			code := rom[a-1]
			byCode[code] = append(byCode[code], site{a, kind})
		}
	}
	var codes []uint8
	for c := range byCode {
		codes = append(codes, c)
	}
	sort.Slice(codes, func(i, j int) bool { return codes[i] < codes[j] })
	names := map[uint8]string{0x0B: "ADC-GND", 0x0D: "OVEN COLD", 0x18: "ADC-2V", 0x23: "FREQ UNCAL", 0x28: "ADC-TIME"}
	for _, c := range codes {
		fmt.Printf("code 0x%02X %-10s:", c, names[c])
		for _, s := range byCode[c] {
			fmt.Printf("  %s@%06X", s.kind, s.addr)
		}
		fmt.Println()
	}
}
