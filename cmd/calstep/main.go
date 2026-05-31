// Command calstep instruction-level traces the PRESET ADC cal (fcn.5E6E8) to
// find WHICH check it fails — the deterministic way to RE the cal, instead of
// guessing the ADC transfer function and eyeballing the blinking annunciator.
// It boots to the operating loop, then single-steps counting visits to the
// cal's decision PCs: the success path (0x5F046 sets $94e4=0xD2D2) vs the fail
// branches (the status/array checks at 0x5EFDC/0x5EFE4/0x5F000 → 0x5F062).
//
//	DYLD_FALLBACK_LIBRARY_PATH=/usr/local/lib go run ./cmd/calstep/
package main

import (
	"fmt"
	"os"
	"sort"

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
	m, _ := machine.New8593A(rom)
	m.CPU.Reset()

	inCal := func(pc uint32) bool { return pc >= 0x5E6E8 && pc <= 0x5F070 }
	lb := emutest.NewLoopBreaker(50)
	// Boot in small chunks until PC first enters the cal region (fcn.5E6E8),
	// so we can single-step the cal AS IT RUNS during boot.
	reached := false
	for done := 0; done < 50_000_000 && !reached; done += 200 {
		if inCal(m.CPU.Reg(cpu.PC)) {
			reached = true
			break
		}
		m.CPU.Run(200)
		lb.Check(m.CPU.Reg(cpu.PC), m.CPU.SetReg)
		if (done/200)%50 == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Run(400)
			m.CPU.SetIRQ(0)
		}
	}
	fmt.Printf("reached cal region=%v at PC=%06X\n", reached, m.CPU.Reg(cpu.PC))

	// Labelled cal decision PCs.
	labels := map[uint32]string{
		0x5E6E8: "cal-entry",
		0x5EF90: "read+2V(sel9F)",
		0x5EFA2: "range>0x1FF-FAIL",
		0x5EFAC: "range<-0x200-FAIL",
		0x5EFDC: "statuspoll(2)-FAIL?",
		0x5EFE4: "tst$948e-FAIL?",
		0x5F000: "statuspoll(0x11)-FAIL?",
		0x5F00C: "read-119-points",
		0x5F046: "SUCCESS($94e4=D2D2)",
		0x5F062: "FAIL-path",
		0x5E840: "FAIL-helper",
		0x5E838: "FAIL($94e4!=D2D2)",
	}
	_ = sort.Ints
	// Single-step the cal, logging each labelled decision PC in ORDER (the
	// path), plus key register/memory state, until the cal sets the success
	// state ($94e4=0xD2D2) or we've seen enough.
	irqN := 0
	const steps = 4_000_000
	logged := 0
	for i := 0; i < steps && logged < 80; i++ {
		pc := m.CPU.Reg(cpu.PC)
		if lbl, ok := labels[pc]; ok {
			fmt.Printf("  [%d] %06X %-22s D0=%08X A6=%06X | 94da=%04X 94e4=%04X 948e=%08X 9492=%06X\n",
				i, pc, lbl, m.CPU.Reg(cpu.D0), m.CPU.Reg(cpu.A6),
				m.Bus.Read(0xFF94DA, bus.Word), m.Bus.Read(0xFF94E4, bus.Word),
				m.Bus.Read(0xFF948E, bus.Long), m.Bus.Read(0xFF9492, 4))
			logged++
			if pc == 0x5F046 {
				fmt.Println("  --> SUCCESS reached")
				break
			}
		}
		if err := m.CPU.Step(); err != nil {
			break
		}
		irqN++
		if irqN >= 2500 {
			m.CPU.SetIRQ(5)
			m.CPU.Step()
			m.CPU.SetIRQ(0)
			irqN = 0
		}
	}
	fmt.Printf("cal vars: 94da=%04X 94e4=%04X 948e=%08X 948c=%04X 94dd=%04X\n",
		m.Bus.Read(0xFF94DA, bus.Word), m.Bus.Read(0xFF94E4, bus.Word),
		m.Bus.Read(0xFF948E, bus.Long), m.Bus.Read(0xFF948C, bus.Word), m.Bus.Read(0xFF94DD, bus.Word))
}
