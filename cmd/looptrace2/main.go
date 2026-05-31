// Proper one-shot sweep-clock: clear f300 bit11 when the firmware re-arms (writes
// sweep DAC 0xFFF716), drive IRQ6 to fill the buffer from the SweepEngine, set
// bit11 ONCE when full (A5>=bf30). This is the faithful handshake fcn.171f6 polls.
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
	setBit11 := func(on bool) {
		v := m.Bus.Read(0xFFF300, bus.Word)
		if on {
			v |= 0x0800
		} else {
			v &^= 0x0800
		}
		m.Bus.Write(0xFFF300, bus.Word, v)
	}
	complete := false
	m.Bus.OnWrite = func(a uint32, sz bus.Size, v uint32) {
		if a == 0xFFF716 && complete { // firmware re-armed → start a new sweep
			complete = false
			m.MMIO.Sweep.Reset()
		}
	}
	lb := emutest.NewLoopBreaker(50)
	sweeps := 0
	for chunk := 0; chunk < 200_000; chunk++ {
		bf30 := rdL(0xFFBF30)
		bf34 := rdL(0xFFBF34)
		if !complete && (bf34 == 0x40B8 || bf34 == 0x410A) && bf30 != 0 {
			for k := 0; k < 8 && m.CPU.Reg(cpu.A5) < bf30; k++ {
				m.CPU.SetIRQ(6)
				m.CPU.Run(250)
				m.CPU.SetIRQ(0)
			}
			if m.CPU.Reg(cpu.A5) >= bf30 {
				complete = true
				setBit11(true)
				sweeps++
			}
		}
		if !complete {
			setBit11(false)
		}
		m.CPU.Run(2000)
		lb.Check(m.CPU.Reg(cpu.PC), m.CPU.SetReg)
		if chunk%5 == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Run(400)
			m.CPU.SetIRQ(0)
		}
	}
	fmt.Printf("sweeps signalled complete: %d  final lines=%d\n", sweeps, m.MMIO.Display.Lines)
	f, _ := os.Create("screens/trace_sweepclock.png")
	png.Encode(f, m.MMIO.Display.RenderFrame())
	f.Close()
	fmt.Println("wrote screens/trace_sweepclock.png")
}
