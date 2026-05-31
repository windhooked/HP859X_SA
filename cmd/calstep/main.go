// Command calstep: (1) trace the cal state machine ($956a/$956e/$94e4) over the
// boot to find the stuck state, and (2) DECISIVE TEST — force the cal-complete
// state ($94e4=0xD2D2 + cal-OK flags) during the operating loop and check
// whether the trace draws / the ADC annunciators change. Determines if the cal
// actually gates the trace before sinking more RE into it.
package main

import (
	"fmt"

	"github.com/windhooked/HP859X_SA/internal/emutest"
	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
	"github.com/windhooked/HP859X_SA/pkg/emu/machine"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

func main() {
	rom, _ := romloader.LoadDir("hp8593a_eeproms")
	m, _ := machine.New8593A(rom)
	m.CPU.Reset()
	rdw := func(a uint32) uint16 { return uint16(m.Bus.Read(a, bus.Word)) }

	lb := emutest.NewLoopBreaker(50)
	// Boot, sampling cal state vars to see how far the boot cal progresses.
	prev := ""
	for done := 0; done < 50_000_000; done += 2000 {
		m.CPU.Run(2000)
		lb.Check(m.CPU.Reg(cpu.PC), m.CPU.SetReg)
		if (done/2000)%5 == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Run(400)
			m.CPU.SetIRQ(0)
		}
		s := fmt.Sprintf("956a=%04X 956e=%04X 94e4=%04X 94dd=%02X", rdw(0xFF956A), rdw(0xFF956E), rdw(0xFF94E4), m.Bus.Read(0xFF94DD, bus.Byte))
		if s != prev {
			fmt.Printf("  @%dM %s\n", done/1_000_000, s)
			prev = s
		}
	}
	lines0 := m.MMIO.Display.Lines

	// DECISIVE TEST: force the cal-complete sentinel + run more, watching Lines.
	fmt.Println("--- forcing 94e4=0xD2D2 (cal-complete sentinel) each chunk ---")
	for k := 0; k < 30000; k++ {
		m.Bus.Write(0xFF94E4, bus.Word, 0xD2D2)
		m.CPU.Run(2000)
		lb.Check(m.CPU.Reg(cpu.PC), m.CPU.SetReg)
		if k%5 == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Run(400)
			m.CPU.SetIRQ(0)
		}
	}
	fmt.Printf("after forcing cal-complete: Lines %d -> %d (trace drawn=%v) 94e4=%04X\n",
		lines0, m.MMIO.Display.Lines, m.MMIO.Display.Lines > lines0+100, rdw(0xFF94E4))
}
