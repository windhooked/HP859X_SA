// Command sweepengine drives a faithful sweep using the MMIO detector-ADC model:
// it sets SweepActive (so 0xFFF200 READS return the synthesized detector — never
// writing the sweep-start latch, which previously caused spurious menu nav), then
// fires IRQ6 as the sweep clock gated on A5<bf30. The firmware's peak handler
// captures the detector into the trace buffer; fcn.CFBE paints each A5-$b1d8
// delta. Resets the detector position when A5 wraps (retrace). Renders to
// ./screens/.
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
	lb := emutest.NewLoopBreaker(50)
	rdL := func(a uint32) uint32 { return m.Bus.Read(a, bus.Long) }
	rdW := func(a uint32) uint16 { return uint16(m.Bus.Read(a, bus.Word)) }

	for done := 0; done < 160_000_000; done += 2000 {
		m.CPU.Run(2000)
		lb.Check(m.CPU.Reg(cpu.PC), m.CPU.SetReg)
		if (done/2000)%5 == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Run(400)
			m.CPU.SetIRQ(0)
		}
	}
	fmt.Printf("booted: Lines=%d b0ec=%04X A5=%08X bf30=%08X bf34=%08X\n",
		m.MMIO.Display.Lines, rdW(0xFFB0EC), m.CPU.Reg(cpu.A5), rdL(0xFFBF30), rdL(0xFFBF34))

	noDrive := os.Getenv("NODRIVE") != ""
	m.MMIO.SweepActive = true
	m.MMIO.SweepPoints = 401
	linesBefore := m.MMIO.Display.Lines
	prevA5 := m.CPU.Reg(cpu.A5)
	for i := 0; i < 200000; i++ {
		m.CPU.Run(700)
		lb.Check(m.CPU.Reg(cpu.PC), m.CPU.SetReg)
		if i%5 == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Run(300)
			m.CPU.SetIRQ(0)
		}
		a5 := m.CPU.Reg(cpu.A5)
		bf30 := rdL(0xFFBF30)
		bs := bf30 - 802
		if a5 < prevA5 { // A5 rewound => firmware re-armed a new sweep (retrace)
			m.MMIO.ResetSweep()
		}
		prevA5 = a5
		if !noDrive && a5 >= bs && a5 < bf30 {
			m.CPU.SetIRQ(6) // sweep clock; handler reads 0xFFF200 (detector)
			m.CPU.Run(250)
			m.CPU.SetIRQ(0)
		}
		if i%20000 == 0 && i > 0 {
			fmt.Printf("  i=%d Lines=%d b0ec=%04X A5=%08X\n", i, m.MMIO.Display.Lines, rdW(0xFFB0EC), m.CPU.Reg(cpu.A5))
		}
		if m.MMIO.Display.Lines > linesBefore+200 {
			fmt.Printf("** big draw at i=%d: Lines %d -> %d\n", i, linesBefore, m.MMIO.Display.Lines)
			break
		}
	}
	fmt.Printf("final: Lines %d -> %d  b0ec=%04X\n", linesBefore, m.MMIO.Display.Lines, rdW(0xFFB0EC))
	if f, err := os.Create("screens/sweepengine.png"); err == nil {
		png.Encode(f, m.MMIO.Display.RenderFrame())
		f.Close()
		fmt.Println("wrote screens/sweepengine.png")
	}
}
