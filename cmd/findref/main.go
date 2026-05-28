// Command findref scans the HP 8593A ROM image (loaded from the *top*.bin
// EEPROM sources) for word-aligned occurrences of a 16-bit big-endian value —
// typically a short-form MMIO address like 0xF752 — and disassembles a window
// around each hit so the accessing instruction can be identified.
//
// Usage:
//
//	go run ./cmd/findref/ <word-hex> [window-bytes]
//	go run ./cmd/findref/ F752 24
package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	musashi "github.com/windhooked/HP859X_SA/pkg/emu/cpu/musashi"
	"github.com/windhooked/HP859X_SA/pkg/emu/device"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: findref <word-hex> [window-bytes]\n")
		os.Exit(1)
	}
	target, err := strconv.ParseUint(os.Args[1], 16, 16)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bad word\n")
		os.Exit(1)
	}
	window := uint32(24)
	if len(os.Args) > 2 {
		if n, err := strconv.ParseUint(os.Args[2], 10, 32); err == nil {
			window = uint32(n)
		}
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

	tw := uint16(target)
	hits := 0
	for off := 0; off+1 < len(img); off += 2 {
		w := uint16(img[off])<<8 | uint16(img[off+1])
		if w != tw {
			continue
		}
		hits++
		at := uint32(off)
		from := at
		if from > window {
			from = at - window
		} else {
			from = 0
		}
		fmt.Printf("--- hit @ %06X ---\n", at)
		for pc := from; pc <= at+4; {
			text, sz := c.Disasm(pc)
			if sz == 0 {
				sz = 2
			}
			marker := "  "
			if pc <= at && pc+sz > at {
				marker = ">>"
			}
			fmt.Printf("%s  %06X  %s\n", marker, pc, text)
			pc += sz
		}
	}
	fmt.Printf("\n%d hit(s) for word %04X\n", hits, tw)
}
