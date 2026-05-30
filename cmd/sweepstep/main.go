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

	// Put a synthetic sample on the detector, fire IRQ6, and single-step the
	// handler. The 68000 takes the autovector; PC jumps to 0x4088. Step until
	// we return to user code (PC leaves the 0x40xx handler region) or 300 steps.
	m.Bus.Write(0xFFF200, bus.Word, 0x0280)
	m.CPU.SetIRQ(6)
	// Step into the exception first.
	prevPC := m.CPU.Reg(cpu.PC)
	for i := 0; i < 400; i++ {
		if err := m.CPU.Step(); err != nil {
			fmt.Printf("step err: %v\n", err)
			break
		}
		m.CPU.SetIRQ(0)
		pc := m.CPU.Reg(cpu.PC)
		// Only log the handler region (0x4080..0x40D0) plus the first entry.
		if pc >= 0x4080 && pc <= 0x40D0 {
			fmt.Printf("  step %3d PC=%06X A5=%08X D7=%04X  [bf30=%08X bf34=%08X befa=%04X]\n",
				i, pc, m.CPU.Reg(cpu.A5), m.CPU.Reg(cpu.D7)&0xFFFF,
				rdL(0xFFBF30), rdL(0xFFBF34), m.Bus.Read(0xFFBEFA, bus.Word))
		}
		// Stop once we've entered then left the handler region.
		if prevPC >= 0x4080 && prevPC <= 0x40D0 && (pc < 0x4080 || pc > 0x40D0) {
			fmt.Printf("  handler returned to PC=%06X after step %d\n", pc, i)
			break
		}
		prevPC = pc
	}
	fmt.Printf("after IRQ6: A5=%08X bf34=%08X befa=%04X\n",
		m.CPU.Reg(cpu.A5), rdL(0xFFBF34), m.Bus.Read(0xFFBEFA, bus.Word))
}
