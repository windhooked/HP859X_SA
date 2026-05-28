// Command sweeprender boots the HP 8593A, runs sweep-armed IRQ6 capture for
// a configurable number of cycles, then forces the operating tick (fcn.18568)
// to fire — testing whether the trace render path runs end-to-end when the
// operating tick is given a chance to execute.
//
// The sweep-done flag (RAM[0xFFBEFA] bit 13) gets set naturally by the
// IRQ6 sample-capture handler at ROM 0x40C2 (the "end-of-sweep" dispatch).
// In the natural operating loop the firmware never processes that flag
// because the same path-A dispatcher obstruction that blocks the key
// consumer (see docs/rom_annotations.md and cmd/keystate) also blocks the
// sweep-done processor at ROM 0x17346. Force-jumping into the operating
// tick is the experimental way to give the firmware a chance to drain
// the flag and render the captured trace.
//
// Usage:
//
//	go run ./cmd/sweeprender/ [cycles] [out.png]
//	  default 80M cycles + 10M force-tick + screens/sweep_render.png
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
	preTickCycles := 80_000_000
	out := "screens/sweep_render.png"
	if len(os.Args) > 1 {
		if n, err := strconv.Atoi(os.Args[1]); err == nil && n > 0 {
			preTickCycles = n
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

	const (
		chunkCycles     = 2000
		breakThresh     = 50
		irq5Period      = 5
		irq6PeriodArmed = 8
		irqServiceCost  = 400
	)
	lb := emutest.NewLoopBreaker(breakThresh)

	rdBF34 := func() uint32 { return m.Bus.Read(0xFFBF34, bus.Long) }
	rdw := func(a uint32) uint32 { return m.Bus.Read(a, bus.Word) }

	sweepArmedAt := -1
	samplePos := 0
	irq6Count := 0

	for done := 0; done < preTickCycles; done += chunkCycles {
		m.CPU.Run(chunkCycles)
		lb.Check(m.CPU.Reg(cpu.PC), m.CPU.SetReg)
		chunk := done / chunkCycles

		if chunk%irq5Period == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Run(irqServiceCost)
			m.CPU.SetIRQ(0)
		}

		armed := rdBF34() == 0x40B8
		if armed && sweepArmedAt == -1 {
			sweepArmedAt = chunk
			fmt.Printf("sweep armed at chunk %d (~%d cycles)\n",
				chunk, chunk*chunkCycles)
		}
		if armed && chunk%irq6PeriodArmed == 0 {
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
	preMoves, preGlyphs, preLines, preDots := d.Moves, d.Glyphs, d.Lines, d.Dots
	prePaints := d.Paints

	fmt.Printf("\nPRE-TICK:\n")
	fmt.Printf("  PC=%06X  bf30=%08X bf34=%08X befa=%04X  A5=%08X\n",
		m.CPU.Reg(cpu.PC),
		m.Bus.Read(0xFFBF30, bus.Long), rdBF34(), rdw(0xFFBEFA),
		m.CPU.Reg(cpu.A5))
	fmt.Printf("  sweep-done flag set? befa.13=%v\n",
		rdw(0xFFBEFA)&(1<<13) != 0)
	fmt.Printf("  IRQ6 ticks=%d  draw: moves=%d glyphs=%d lines=%d dots=%d paints=%d\n",
		irq6Count, preMoves, preGlyphs, preLines, preDots, prePaints)

	fmt.Printf("\n>>> Forcing operating tick (PC=0x18568) <<<\n")
	endPC := m.ForceOperatingTick(10_000_000)

	fmt.Printf("\nPOST-TICK:\n")
	fmt.Printf("  end PC=%06X  bf30=%08X bf34=%08X befa=%04X\n",
		endPC,
		m.Bus.Read(0xFFBF30, bus.Long), rdBF34(), rdw(0xFFBEFA))
	fmt.Printf("  sweep-done flag still set? befa.13=%v\n",
		rdw(0xFFBEFA)&(1<<13) != 0)
	fmt.Printf("  draw delta: moves=%+d glyphs=%+d lines=%+d dots=%+d paints=%+d\n",
		d.Moves-preMoves, d.Glyphs-preGlyphs, d.Lines-preLines, d.Dots-preDots, d.Paints-prePaints)

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
	fmt.Printf("\nwrote %s\n", out)
}
