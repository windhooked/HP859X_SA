// Model 0xFFF300 bit 11 as the real sweep-complete signal: assert when the trace
// buffer is full (A5>=bf30), clear while sweeping. Drive IRQ6 to fill. This is
// the proper sweep handshake the operating-loop idle wait (0x188b6) polls; with
// it the firmware should process the completed sweep and draw the trace.
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
	rom, _ := romloader.LoadDir("hp8593a_eeproms")
	m, _ := machine.New8593A(rom)
	m.CPU.Reset()
	m.BootToOperating(165_000_000)
	m.MMIO.SweepActive = true
	rdL := func(a uint32) uint32 { return m.Bus.Read(a, bus.Long) }
	setF300bit11 := func(on bool) {
		v := m.Bus.Read(0xFFF300, bus.Word)
		if on {
			v |= 0x0800
		} else {
			v &^= 0x0800
		}
		m.Bus.Write(0xFFF300, bus.Word, v)
	}
	lb := emutest.NewLoopBreaker(50)
	reachDraw, reach18910 := 0, 0
	startLines := m.MMIO.Display.Lines
	for chunk := 0; chunk < 120_000; chunk++ {
		bf30 := rdL(0xFFBF30)
		bf34 := rdL(0xFFBF34)
		full := bf30 != 0 && m.CPU.Reg(cpu.A5) >= bf30
		setF300bit11(full) // sweep-complete iff buffer full
		for s := 0; s < 8; s++ {
			pc := m.CPU.Reg(cpu.PC)
			if pc == 0x18910 {
				reach18910++
			}
			if pc == 0x65986 {
				reachDraw++
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
		// fill the buffer while sweeping (not full)
		if (bf34 == 0x40B8 || bf34 == 0x410A) && !full {
			for k := 0; k < 6 && m.CPU.Reg(cpu.A5) < bf30; k++ {
				m.CPU.SetIRQ(6)
				m.CPU.Run(250)
				m.CPU.SetIRQ(0)
			}
		}
	}
	fmt.Printf("work-path 0x18910: %d   __GTTDRW 0x65986: %d   lines %d->%d\n",
		reach18910, reachDraw, startLines, m.MMIO.Display.Lines)
	f, _ := os.Create("screens/trace_handshake.png")
	png.Encode(f, m.MMIO.Display.RenderFrame())
	f.Close()
	fmt.Println("wrote screens/trace_handshake.png")
}
