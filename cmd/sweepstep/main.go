// Command sweepstep single-steps one IRQ6 (sweep-sample-capture) handler on the
// healthy firmware to understand the trace-buffer write-pointer (A5) /
// buffer-end (bf30) / dispatch (bf34) coordination — i.e. why A5 overruns the
// trace buffer and the firmware never draws the trace. It boots to the
// armed-sweep state, then fires IRQ6 and logs each instruction of the handler
// (PC, A5, and the bf30 value the capture path compares against).
//
//	DYLD_FALLBACK_LIBRARY_PATH=/usr/local/lib go run ./cmd/sweepstep/
package main

import (
	"fmt"
	"os"

	"github.com/windhooked/HP859X_SA/internal/emutest"
	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
	"github.com/windhooked/HP859X_SA/pkg/emu/machine"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

func main() {
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
		chunkCycles    = 2000
		irq5Period     = 5
		irqServiceCost = 400
	)
	lb := emutest.NewLoopBreaker(50)
	rdL := func(a uint32) uint32 { return m.Bus.Read(a, bus.Long) }

	// Boot to the armed-sweep state.
	armed := false
	for done := 0; done < 60_000_000 && !armed; done += chunkCycles {
		m.CPU.Run(chunkCycles)
		lb.Check(m.CPU.Reg(cpu.PC), m.CPU.SetReg)
		if (done/chunkCycles)%irq5Period == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Run(irqServiceCost)
			m.CPU.SetIRQ(0)
		}
		if rdL(0xFFBF34) == 0x40B8 {
			armed = true
		}
	}
	if !armed {
		fmt.Println("sweep never armed")
		os.Exit(1)
	}
	fmt.Printf("armed: A5=%08X bf30=%08X bf34=%08X befa=%04X\n",
		m.CPU.Reg(cpu.A5), rdL(0xFFBF30), rdL(0xFFBF34), m.Bus.Read(0xFFBEFA, bus.Word))

	// Fill the trace buffer: fire IRQ6 with synthetic samples until A5 reaches
	// bf30 (sweep complete, befa bit13 set), gating on A5 < bf30 so we don't
	// overrun. Between captures let the firmware run a little.
	bf30 := rdL(0xFFBF30)
	caps := 0
	for n := 0; n < 1000 && m.CPU.Reg(cpu.A5) < bf30; n++ {
		m.Bus.Write(0xFFF200, bus.Word, 0x0200+uint32(n%200))
		m.CPU.SetIRQ(6)
		m.CPU.Run(400)
		m.CPU.SetIRQ(0)
		m.CPU.Run(2000) // let the operating loop run between samples
		caps++
	}
	fmt.Printf("sweep filled: %d captures, A5=%08X bf30=%08X bf34=%08X befa=%04X (bit13=%v)\n",
		caps, m.CPU.Reg(cpu.A5), bf30, rdL(0xFFBF34), m.Bus.Read(0xFFBEFA, bus.Word),
		m.Bus.Read(0xFFBEFA, bus.Word)&0x2000 != 0)

	// Now single-step the operating loop and see whether the firmware ever
	// reaches the DLP-driven sweep-trace processing (0x5EC00..0x5EE00), the DLP
	// trace source (0x5FA00..0x5FB00), or processes sweep-done (clears befa
	// bit13 / changes bf34 / draws trace lines).
	linesBefore := m.MMIO.Display.Lines
	befaBefore := m.Bus.Read(0xFFBEFA, bus.Word)
	bf34Before := rdL(0xFFBF34)
	hits := map[uint32]int{}
	const steps = 3_000_000
	irqN := 0
	for i := 0; i < steps; i++ {
		if err := m.CPU.Step(); err != nil {
			break
		}
		pc := m.CPU.Reg(cpu.PC)
		page := pc >> 8
		if (page >= 0x5EC && page <= 0x5EE) || page == 0x5FA || (pc >= 0x2AB8 && pc <= 0x2B1C) {
			hits[page]++
		}
		irqN++
		if irqN >= 2500 { // periodic IRQ5 to keep the timer alive
			m.CPU.SetIRQ(5)
			m.CPU.Step()
			m.CPU.SetIRQ(0)
			irqN = 0
		}
	}
	fmt.Printf("after %d operating-loop steps:\n", steps)
	fmt.Printf("  sweep-trace region visits: %v\n", hits)
	fmt.Printf("  Lines: %d -> %d (trace drawn = %v)\n", linesBefore, m.MMIO.Display.Lines, m.MMIO.Display.Lines > linesBefore+50)
	fmt.Printf("  befa: %04X -> %04X (bit13 %v->%v)\n", befaBefore, m.Bus.Read(0xFFBEFA, bus.Word),
		befaBefore&0x2000 != 0, m.Bus.Read(0xFFBEFA, bus.Word)&0x2000 != 0)
	fmt.Printf("  bf34: %08X -> %08X  A5=%08X  PC=%06X\n",
		bf34Before, rdL(0xFFBF34), m.CPU.Reg(cpu.A5), m.CPU.Reg(cpu.PC))
}
