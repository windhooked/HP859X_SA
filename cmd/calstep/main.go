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

	inCal := func(pc uint32) bool { return pc >= 0x5E300 && pc <= 0x5F200 }
	labels := map[uint32]string{
		0x5E6E8: "PhaseA-entry", 0x5E71E: "statusgate", 0x5E838: "A-bail(0xFFFF)",
		0x5E840: "A-statusfail(0)", 0x5EF96: "PhaseB-read+2V", 0x5EFA2: "B-range>FAIL",
		0x5F00C: "B-read119pts", 0x5F046: "B-SUCCESS(94e4=D2D2)", 0x5F062: "B-FAIL",
		0x5EFDC: "B-stat2", 0x5EFE4: "B-tst948e", 0x5F000: "B-stat11",
	}
	lb := emutest.NewLoopBreaker(50)
	rdw := func(a uint32) uint16 { return uint16(m.Bus.Read(a, bus.Word)) }
	// Boot in small chunks; whenever PC is in the cal region, single-step
	// through that cal call logging the labelled decision PCs + $94e4, then
	// resume chunked boot. Traces the WHOLE boot-time cal sequence.
	logged := 0
	for done := 0; done < 60_000_000 && logged < 120; done += 200 {
		if inCal(m.CPU.Reg(cpu.PC)) {
			for j := 0; j < 8000 && inCal(m.CPU.Reg(cpu.PC)) && logged < 120; j++ {
				pc := m.CPU.Reg(cpu.PC)
				if lbl, ok := labels[pc]; ok {
					caller := ""
					if pc == 0x5E6E8 {
						caller = fmt.Sprintf(" caller=%06X", m.Bus.Read(m.CPU.Reg(cpu.A7), bus.Long)-4)
					}
					fmt.Printf("  %06X %-22s 94da=%04X 94e4=%04X 94dd=%04X 9492=%06X D0=%08X%s\n",
						pc, lbl, rdw(0xFF94DA), rdw(0xFF94E4), rdw(0xFF94DD), m.Bus.Read(0xFF9492, 4), m.CPU.Reg(cpu.D0), caller)
					logged++
				}
				if m.CPU.Step() != nil {
					break
				}
			}
			continue
		}
		m.CPU.Run(200)
		lb.Check(m.CPU.Reg(cpu.PC), m.CPU.SetReg)
		if (done/200)%50 == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Run(400)
			m.CPU.SetIRQ(0)
		}
	}
	fmt.Printf("(boot cal trace: %d labelled events)\n", logged)
}
