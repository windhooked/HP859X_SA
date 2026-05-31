// Clean continuous-sweep drive: clear the stale key flag, model 0xFFF300 bit 11
// as sweep-complete (assert on buffer full, firmware acks by writing f300),
// drive IRQ6 to fill from the SweepEngine, and render — to get fcn.171f6 to draw
// the trace without the firmware menu-walking on a stale key.
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
	rdW := func(a uint32) uint16 { return uint16(m.Bus.Read(a, bus.Word)) }
	lb := emutest.NewLoopBreaker(50)
	reach171f6, reachDraw := 0, 0
	for chunk := 0; chunk < 200_000; chunk++ {
		m.Bus.Write(0xFFBC67, bus.Byte, 0) // keep the key flag clear (no menu-walk)
		bf30 := rdL(0xFFBF30)
		bf34 := rdL(0xFFBF34)
		full := bf30 != 0 && m.CPU.Reg(cpu.A5) >= bf30
		// assert f300 bit11 when full, else clear (firmware acks by writing it)
		v := rdW(0xFFF300)
		if full {
			v |= 0x0800
		} else {
			v &^= 0x0800
		}
		m.Bus.Write(0xFFF300, bus.Word, uint32(v))
		for s := 0; s < 8; s++ {
			pc := m.CPU.Reg(cpu.PC)
			if pc == 0x171F6 {
				reach171f6++
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
		if (bf34 == 0x40B8 || bf34 == 0x410A) && !full {
			for k := 0; k < 6 && m.CPU.Reg(cpu.A5) < bf30; k++ {
				m.CPU.SetIRQ(6)
				m.CPU.Run(250)
				m.CPU.SetIRQ(0)
			}
		}
	}
	fmt.Printf("fcn.171f6 reached: %d   __GTTDRW: %d   lines=%d\n", reach171f6, reachDraw, m.MMIO.Display.Lines)
	f, _ := os.Create("screens/trace_clean.png")
	png.Encode(f, m.MMIO.Display.RenderFrame())
	f.Close()
	fmt.Println("wrote screens/trace_clean.png")
}
