// Command sweeprun boots the HP 8593A with continuous IRQ6 + synthetic ADC
// stimulus woven into the main loop (not as a post-boot burst). The goal is
// to drive the firmware through enough sweep cycles that it processes the
// sweep-done flag (FFBEFA bit 13), resets the trace buffer, and renders the
// captured trace to screen.
//
// Usage:
//
//	go run ./cmd/sweeprun/ [cycles] [out.png]   # default 200M cycles, screens/sweep_run.png
package main

import (
	"fmt"
	"image/png"
	"os"
	"strconv"

	"github.com/windhooked/HP859X_SA/internal/emutest"
	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
	"github.com/windhooked/HP859X_SA/pkg/emu/machine"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

func main() {
	totalCycles := 200_000_000
	out := "screens/sweep_run.png"
	if len(os.Args) > 1 {
		if n, err := strconv.Atoi(os.Args[1]); err == nil && n > 0 {
			totalCycles = n
		}
	}
	if len(os.Args) > 2 {
		out = os.Args[2]
	}

	rom, err := romloader.LoadDir("hp8593a_eeproms")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	m, err := machine.New8593A(rom)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	m.CPU.Reset()

	// Mirror the canonical BootToOperating loop but ALSO inject IRQ6 (with a
	// synthetic ADC sample at 0xFFF200) once the firmware has armed the sweep
	// (FFBF34 == 0x40B8, the capture handler).
	const (
		chunkCycles     = 2000
		breakThresh     = 50
		irq5Period      = 5 // every 5 chunks, fire IRQ5 (timer)
		irq6PeriodArmed = 8 // every 8 chunks, fire IRQ6 (sample) — once sweep armed
		irqServiceCost  = 400
	)
	lb := emutest.NewLoopBreaker(breakThresh)

	rdBF34 := func() uint32 { return m.Bus.Read(0xFFBF34, bus.Long) }
	rd := func(a uint32) uint32 { return m.Bus.Read(a, bus.Long) }
	rdw := func(a uint32) uint32 { return m.Bus.Read(a, bus.Word) }

	sweepArmedAt := -1
	samplePos := 0
	irq6Count := 0

	for done := 0; done < totalCycles; done += chunkCycles {
		m.CPU.Run(chunkCycles)
		lb.Check(m.CPU.Reg(cpu.PC), m.CPU.SetReg)
		chunk := done / chunkCycles

		// IRQ5 timer tick — same cadence as BootToOperating.
		if chunk%irq5Period == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Run(irqServiceCost)
			m.CPU.SetIRQ(0)
		}

		// IRQ6 sample capture — only once the firmware has armed the sweep.
		armed := rdBF34() == 0x40B8
		if armed && sweepArmedAt == -1 {
			sweepArmedAt = chunk
			fmt.Printf("sweep armed at chunk %d (~%d cycles); bf30=%08X bf34=%08X\n",
				chunk, chunk*chunkCycles, rd(0xFFBF30), rdBF34())
		}
		if armed && chunk%irq6PeriodArmed == 0 {
			// Synthetic ADC sample: noise floor + peak at sample position 200.
			v := uint32(0x0140)
			d := samplePos - 200
			if d < 0 {
				d = -d
			}
			if d < 25 {
				v += uint32(25-d) * 0x60
			}
			m.Bus.Write(0xFFF200, bus.Word, v)
			samplePos = (samplePos + 1) % 401
			m.CPU.SetIRQ(6)
			m.CPU.Run(irqServiceCost)
			m.CPU.SetIRQ(0)
			irq6Count++
		}
	}

	d := m.MMIO.Display
	fmt.Printf("\n=== Result ===\n")
	fmt.Printf("  total cycles=%d  IRQ6 ticks=%d  sweep armed at chunk %d\n",
		totalCycles, irq6Count, sweepArmedAt)
	fmt.Printf("  final PC=%06X  SR=%04X  A5=%08X\n",
		m.CPU.Reg(cpu.PC), m.CPU.Reg(cpu.SR), m.CPU.Reg(cpu.A5))
	fmt.Printf("  bf30=%08X bf34=%08X befa=%04X\n",
		rd(0xFFBF30), rdBF34(), rdw(0xFFBEFA))
	fmt.Printf("  draw counts: moves=%d glyphs=%d lines=%d rects=%d dots=%d\n",
		d.Moves, d.Glyphs, d.Lines, d.Rects, d.Dots)

	if err := os.MkdirAll("screens", 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	f, err := os.Create(out)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer f.Close()
	if err := png.Encode(f, d.RenderFrame()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("  wrote %s\n", out)
}
