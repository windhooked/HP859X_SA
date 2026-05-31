// Command tracehunt drives the armed sweep to COMPLETION (fills the trace buffer
// to bf30) and observes whether sweep-done (befa bit13) fires and the firmware
// then schedules/runs the trace-draw DLP (__GTTDRW 0x65986).
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
	m.BootToOperating(165_000_000)
	rdL := func(a uint32) uint32 { return m.Bus.Read(a, bus.Long) }
	rdW := func(a uint32) uint16 { return uint16(m.Bus.Read(a, bus.Word)) }
	m.MMIO.SweepActive = true
	lb := emutest.NewLoopBreaker(50)

	sweeps, drawReached := 0, 0
	befaSeen := false
	for chunk := 0; chunk < 120_000; chunk++ {
		for s := 0; s < 4; s++ {
			if m.CPU.Reg(cpu.PC) == 0x65986 {
				drawReached++
			}
			if m.CPU.Step() != nil {
				break
			}
		}
		m.CPU.Run(2000)
		lb.Check(m.CPU.Reg(cpu.PC), m.CPU.SetReg)
		if chunk%5 == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Run(400)
			m.CPU.SetIRQ(0)
		}
		bf34 := rdL(0xFFBF34)
		if bf34 == 0x40B8 || bf34 == 0x410A {
			// aggressively fill to completion
			for k := 0; k < 8 && m.CPU.Reg(cpu.A5) < rdL(0xFFBF30); k++ {
				m.CPU.SetIRQ(6)
				m.CPU.Run(250)
				m.CPU.SetIRQ(0)
			}
		}
		if rdW(0xFFBEFA)&0x2000 != 0 {
			befaSeen = true
		}
		// detect a completed sweep (A5 reached/exceeded bf30) then reset for next
		if m.CPU.Reg(cpu.A5) >= rdL(0xFFBF30) && rdL(0xFFBF30) != 0 {
			sweeps++
		}
	}
	fmt.Printf("sweeps completed (A5>=bf30): %d\n", sweeps)
	fmt.Printf("befa bit13 (sweep-done) ever set: %v\n", befaSeen)
	fmt.Printf("__GTTDRW (0x65986) reached: %d times\n", drawReached)
	fmt.Printf("final: A5=%06X bf30=%06X bf34=%06X befa=%04X  display lines=%d\n",
		m.CPU.Reg(cpu.A5), rdL(0xFFBF30), rdL(0xFFBF34), rdW(0xFFBEFA), m.MMIO.Display.Lines)
}
