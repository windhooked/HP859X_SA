// Command calgate tests whether $bf2a bit13 (the auto-cal precondition flag,
// read by slot 0x15a -> fcn.5eca2) is THE gate that unblocks the cal chain:
// DAC cal (0x48316) -> fcn.5ED7E -> $94e4=0xD2D2 -> operating/sweep mode -> trace.
// It boots while forcing $bf2a bit13 set, tracks reachability of the cal-validate
// writer (0x5F046) and the sweep state machine (0x5ECEE), reports $94e4 + Lines,
// and renders to ./screens/.
package main

import (
	"fmt"
	"image/png"
	"os"

	"github.com/windhooked/HP859X_SA/internal/emutest"
	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
	"github.com/windhooked/HP859X_SA/pkg/emu/machine"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

func main() {
	force := os.Getenv("NOFORCE") == ""
	rom, _ := romloader.LoadDir("hp8593a_eeproms")
	m, _ := machine.New8593A(rom)
	m.CPU.Reset()
	lb := emutest.NewLoopBreaker(50)
	rdW := func(a uint32) uint16 { return uint16(m.Bus.Read(a, bus.Word)) }
	rdL := func(a uint32) uint32 { return m.Bus.Read(a, bus.Long) }

	d2d2set, validateRun, sweepSM := 0, 0, 0
	for done := 0; done < 200_000_000; done += 2000 {
		if force {
			v := rdL(0xFFBF2A)
			m.Bus.Write(0xFFBF2A, bus.Long, v|(1<<13))
		}
		// short single-step burst to track cal-chain reachability
		for s := 0; s < 8; s++ {
			if m.CPU.Step() != nil {
				break
			}
			pc := m.CPU.Reg(cpu.PC)
			switch {
			case pc == 0x5F046:
				d2d2set++
			case pc >= 0x5EFC0 && pc < 0x5F002:
				validateRun++
			case pc >= 0x5ECEE && pc < 0x5ED80:
				sweepSM++
			}
		}
		m.CPU.Run(2000)
		lb.Check(m.CPU.Reg(cpu.PC), m.CPU.SetReg)
		if (done/2000)%5 == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Run(400)
			m.CPU.SetIRQ(0)
		}
	}
	fmt.Printf("force=%v  validate-routine(0x5EFC0) hits=%d  D2D2-writer(0x5F046) hits=%d  sweepSM hits=%d\n",
		force, validateRun, d2d2set, sweepSM)
	fmt.Printf("  94e4=%04X (D2D2=pass)  94da=%04X  Lines=%d  bf2a=%08X\n",
		rdW(0xFF94E4), rdW(0xFF94DA), m.MMIO.Display.Lines, rdL(0xFFBF2A))
	out := "screens/calgate_forced.png"
	if !force {
		out = "screens/calgate_noforce.png"
	}
	if f, err := os.Create(out); err == nil {
		png.Encode(f, m.MMIO.Display.RenderFrame())
		f.Close()
		fmt.Println("wrote", out)
	}
}
