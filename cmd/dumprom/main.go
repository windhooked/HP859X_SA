// Command dumprom writes the canonical 8593A ROM image, reconstructed from the
// gold *top*.bin EEPROM sources via romloader (never the committed rom.bin), to
// a file for external tooling (rizin/Ghidra/objdump).
//
// Usage:
//
//	go run ./cmd/dumprom/ [out.bin]   # default rom_gold.bin
package main

import (
	"fmt"
	"os"

	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

func main() {
	out := "rom_gold.bin"
	if len(os.Args) > 1 {
		out = os.Args[1]
	}
	img, err := romloader.LoadDir("hp8593a_eeproms")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(out, img, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	sp := uint32(img[0])<<24 | uint32(img[1])<<16 | uint32(img[2])<<8 | uint32(img[3])
	pc := uint32(img[4])<<24 | uint32(img[5])<<16 | uint32(img[6])<<8 | uint32(img[7])
	fmt.Printf("wrote %s (%d bytes); reset SP=%08X PC=%08X\n", out, len(img), sp, pc)
}
