// Command dispatch resolves a Rev L firmware dispatch-table slot to its
// target address and a short disassembly window so the runtime path of
// `jsr $XXX.w` instructions can be followed without manual ROM hex math.
//
// The dispatch table sits at ROM offsets 0x000C0 — 0x007B0+; see
// docs/rom_annotations.md "Firmware dispatch jump table". Each entry is
// a 6-byte `jmp $longabs.l` (`4EF9 hi lo`). Given a slot offset this
// tool prints the JMP, the target, and the first few instructions at the
// target — enough to identify the subsystem you've landed in.
//
// Usage:
//
//	go run ./cmd/dispatch/ <slot_hex>        # print one entry
//	go run ./cmd/dispatch/ <from> <to>       # print a range of entries
//
// Examples:
//
//	go run ./cmd/dispatch/ 148               # key consumer (0x18568)
//	go run ./cmd/dispatch/ C4 200            # the whole low half
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
	if len(os.Args) < 2 || len(os.Args) > 3 {
		fmt.Fprintf(os.Stderr, "usage: dispatch <slot_hex> [end_slot_hex]\n")
		os.Exit(1)
	}
	from, err := strconv.ParseUint(os.Args[1], 16, 32)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bad slot %q\n", os.Args[1])
		os.Exit(1)
	}
	to := from
	if len(os.Args) == 3 {
		t, err := strconv.ParseUint(os.Args[2], 16, 32)
		if err != nil {
			fmt.Fprintf(os.Stderr, "bad end-slot %q\n", os.Args[2])
			os.Exit(1)
		}
		to = t
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

	// Round `from` to a 6-byte boundary if it isn't already aligned. The
	// JMP entries start at 0xC0 + n*6, but the IRQ vectors at 0x0–0xBF
	// are 4-byte longwords; if a caller passes a 4-byte-aligned slot in
	// that region we just print the longword.
	for slot := from; slot <= to; {
		// Try the longword interpretation first (IRQ vector region).
		if slot < 0xC0 {
			word := uint32(img[slot])<<24 | uint32(img[slot+1])<<16 |
				uint32(img[slot+2])<<8 | uint32(img[slot+3])
			fmt.Printf("%03X  vector → %08X\n", slot, word)
			slot += 4
			continue
		}

		// JMP entries: must start with `4E F9` (opcode for jmp.l with abs.l
		// addressing). If not, this slot isn't a JMP — print the raw word
		// instead and bump by 2.
		if img[slot] != 0x4E || img[slot+1] != 0xF9 {
			word := uint16(img[slot])<<8 | uint16(img[slot+1])
			fmt.Printf("%03X  data  %04X\n", slot, word)
			slot += 2
			continue
		}

		target := uint32(img[slot+2])<<24 | uint32(img[slot+3])<<16 |
			uint32(img[slot+4])<<8 | uint32(img[slot+5])
		fmt.Printf("%03X  jmp $%06X\n", slot, target)

		// Disassemble the first two instructions at the target.
		if to == from {
			pc := target
			for i := 0; i < 3 && pc < uint32(len(img)); i++ {
				ins, n := c.Disasm(pc)
				fmt.Printf("       %06X  %s\n", pc, ins)
				if n == 0 {
					break
				}
				pc += n
			}
		}

		slot += 6
	}
}
