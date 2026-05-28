// Command tracestall — runs the HP 8593A machine with the LoopBreaker,
// then disassembles the region around the final PC and any requested
// extra addresses (e.g. IRQ handlers).
//
// Usage:
//
//	go run ./cmd/tracestall/ [cycles]   # default 10 000 000
package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/windhooked/HP859X_SA/internal/emutest"
	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
	musashi "github.com/windhooked/HP859X_SA/pkg/emu/cpu/musashi"
	"github.com/windhooked/HP859X_SA/pkg/emu/device"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

func disasmRegion(c *musashi.CPU, from, to uint32, markPC uint32) {
	for pc := from; pc < to; {
		text, sz := c.Disasm(pc)
		if sz == 0 {
			sz = 2
		}
		marker := "   "
		if pc == markPC {
			marker = ">>>"
		}
		fmt.Printf("%s  %08X  %s\n", marker, pc, text)
		pc += sz
	}
}

func main() {
	totalCycles := 10_000_000
	if len(os.Args) > 1 {
		n, err := strconv.Atoi(os.Args[1])
		if err != nil || n <= 0 {
			fmt.Fprintf(os.Stderr, "usage: tracestall [cycles]\n")
			os.Exit(1)
		}
		totalCycles = n
	}

	img, err := romloader.LoadDir("hp8593a_eeproms")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	b := &bus.Bus{}
	b.OnFault = func(addr uint32, sz bus.Size, write bool) uint32 { return 0 }
	b.Map(0x000000, uint32(len(img)), "ROM", bus.NewROM(img))
	b.Map(0xEF8000, 0x000100, "PIT", bus.NewRAM(0x000100)) // MC68230 PIT stub
	b.Map(0xFEC000, 0x004000, "TestRAM", bus.NewRAM(0x004000))
	b.Map(0xFF0000, 0x00F000, "RAM", bus.NewRAM(0x00F000))
	b.Map(device.MMIOBase, device.MMIOSize, "MMIO", device.NewHP8593AMMIO())

	c, _ := musashi.New(b)
	c.Reset()

	const chunkCycles = 2000
	const breakThresh = 50

	// irqPeriod: fire IRQ5 every N chunks (simulates the hardware timer tick).
	// The timer wait at 0x36D66 polls RAM[0xFFBFCA] which is incremented by IRQ5
	// handler at 0x19E2. At IRQ5 every 5 chunks (= 10 000 cycles) and a 30-tick
	// timeout, the wait exits after ~300 000 cycles.
	const irqPeriod = 5 // fire IRQ5 every 5 chunks
	const irqServiceCycles = 400

	lb := emutest.NewLoopBreaker(breakThresh)
	breaks := 0
	irqsFired := 0

	for done := 0; done < totalCycles; done += chunkCycles {
		c.Run(chunkCycles)
		pc := c.Reg(cpu.PC)
		if lb.Check(pc, c.SetReg) {
			breaks++
		}

		// Periodic IRQ5: simulate the hardware timer tick.
		// Assert IRQ5, run a short burst for the handler to service it, deassert.
		chunk := done / chunkCycles
		if chunk%irqPeriod == 0 {
			c.SetIRQ(5)
			c.Run(irqServiceCycles)
			c.SetIRQ(0)
			irqsFired++
		}
	}

	finalPC := c.Reg(cpu.PC)
	fmt.Printf("=== %d cycles, %d loop breaks, %d IRQ5s, final PC=%08X ===\n",
		totalCycles, breaks, irqsFired, finalPC)
	fmt.Printf("SR = %08X\n", c.Reg(cpu.SR))

	fmt.Printf("\nRegisters:\n")
	for _, r := range []struct {
		n string
		r cpu.Reg
	}{
		{"D0", cpu.D0}, {"D1", cpu.D1}, {"D2", cpu.D2}, {"D3", cpu.D3},
		{"D4", cpu.D4}, {"D5", cpu.D5}, {"D6", cpu.D6}, {"D7", cpu.D7},
		{"A0", cpu.A0}, {"A1", cpu.A1}, {"A2", cpu.A2}, {"A3", cpu.A3},
		{"A4", cpu.A4}, {"A5", cpu.A5}, {"A6", cpu.A6}, {"A7", cpu.A7},
		{"PC", cpu.PC}, {"SR", cpu.SR},
	} {
		fmt.Printf("  %-2s = %08X\n", r.n, c.Reg(r.r))
	}

	fmt.Printf("\n=== Disasm around PC=%08X ===\n", finalPC)
	disasmRegion(c, finalPC-80, finalPC+160, finalPC)

	// Disassemble key IRQ handlers from the autovector table.
	fmt.Printf("\n=== IRQ handler vectors (from exception table) ===\n")
	for irq := 1; irq <= 7; irq++ {
		vecAddr := uint32(0x64 + (irq-1)*4) // autovector 1..7 at 0x64–0x7C
		vec := b.Read(vecAddr, bus.Long)
		fmt.Printf("  IRQ%d vector: [%06X] = %06X\n", irq, vecAddr, vec)
		if vec >= 0x100 && vec < 0x80000 {
			fmt.Printf("  --- IRQ%d handler at %06X ---\n", irq, vec)
			disasmRegion(c, vec, vec+160, ^uint32(0))
		}
	}
}
