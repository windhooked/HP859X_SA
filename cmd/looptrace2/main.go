// Drive ONE sweep to completion and render right after fcn.171f6 processes it,
// to see whether the trace line draws (before any later menu navigation).
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
	lb := emutest.NewLoopBreaker(50)
	dots0, lines0 := m.MMIO.Display.Dots, m.MMIO.Display.Lines

	for sweep := 0; sweep < 3; sweep++ {
		// fill the buffer from the SweepEngine
		bf30 := rdL(0xFFBF30)
		for m.CPU.Reg(cpu.A5) < bf30 {
			m.CPU.SetIRQ(6)
			m.CPU.Run(250)
			m.CPU.SetIRQ(0)
			if m.CPU.Reg(cpu.A5) == 0 {
				break
			}
		}
		// signal sweep-complete
		m.Bus.Write(0xFFF300, bus.Word, m.Bus.Read(0xFFF300, bus.Word)|0x0800)
		// let the firmware process+draw, then re-arm clears it
		for c := 0; c < 8000; c++ {
			m.CPU.Run(2000)
			lb.Check(m.CPU.Reg(cpu.PC), m.CPU.SetReg)
			if c%5 == 0 {
				m.CPU.SetIRQ(5)
				m.CPU.Run(400)
				m.CPU.SetIRQ(0)
			}
		}
		fmt.Printf("sweep %d: A5=%06X dots=%d lines=%d\n", sweep, m.CPU.Reg(cpu.A5), m.MMIO.Display.Dots, m.MMIO.Display.Lines)
		png.Encode(mustCreate(fmt.Sprintf("screens/trace_1sweep_%d.png", sweep)), m.MMIO.Display.RenderFrame())
	}
	fmt.Printf("delta dots=%d lines=%d\n", m.MMIO.Display.Dots-dots0, m.MMIO.Display.Lines-lines0)
}

func mustCreate(p string) *os.File { f, _ := os.Create(p); return f }
