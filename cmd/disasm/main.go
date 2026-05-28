// Command disasm disassembles a range of the HP 8593A ROM image, loaded from
// the *top*.bin EEPROM source files (never the generated rom.bin).
//
// Usage:
//
//	go run ./cmd/disasm/ <from-hex> <to-hex>
//	go run ./cmd/disasm/ 7340 7440
package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
	musashi "github.com/windhooked/HP859X_SA/pkg/emu/cpu/musashi"
	"github.com/windhooked/HP859X_SA/pkg/emu/device"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintf(os.Stderr, "usage: disasm <from-hex> <to-hex>\n")
		os.Exit(1)
	}
	from, err1 := strconv.ParseUint(os.Args[1], 16, 32)
	to, err2 := strconv.ParseUint(os.Args[2], 16, 32)
	if err1 != nil || err2 != nil || to <= from {
		fmt.Fprintf(os.Stderr, "bad range\n")
		os.Exit(1)
	}

	img, err := romloader.LoadDir("hp8593a_eeproms")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	b := &bus.Bus{}
	b.OnFault = func(addr uint32, sz bus.Size, write bool) uint32 { return 0 }
	b.Map(0x000000, uint32(len(img)), "ROM", bus.NewROM(img))
	b.Map(0xFEC000, 0x004000, "TestRAM", bus.NewRAM(0x004000))
	b.Map(0xFF0000, 0x00F000, "RAM", bus.NewRAM(0x00F000))
	b.Map(device.MMIOBase, device.MMIOSize, "MMIO", device.NewHP8593AMMIO())

	c, _ := musashi.New(b)
	_ = cpu.PC

	for pc := uint32(from); pc < uint32(to); {
		text, sz := c.Disasm(pc)
		if sz == 0 {
			sz = 2
		}
		fmt.Printf("  %06X  %s\n", pc, text)
		pc += sz
	}
}
