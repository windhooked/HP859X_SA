// Command specmode isolates the trace-paint display-mode gate. 0x7d84/0x7cd6
// (the trace paint, called from the measurement loop fcn.CFBE) test $b0ec
// against spectrum modes 0x2d/0x31/0x36. This boots, renders + dumps $b0ec/$b116
// BEFORE any sweep driving (natural operating UI), then drives a clean bounded
// sweep while tracking $b0ec changes and whether 0x7d84/0x7cd6 are reached, and
// renders AFTER. Pinpoints whether the firmware starts in spectrum mode and what
// (if anything) knocks it out. Renders to ./screens/.
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

func render(m *machine.Machine, name string) {
	if f, err := os.Create("screens/" + name); err == nil {
		png.Encode(f, m.MMIO.Display.RenderFrame())
		f.Close()
		fmt.Println("wrote screens/" + name)
	}
}

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
	fmt.Printf("POST-BOOT: b0ec=%04X b116=%04X Lines=%d (spectrum modes=0x2d/0x31/0x36)\n",
		rdW(0xFFB0EC), rdW(0xFFB116), m.MMIO.Display.Lines)
	render(m, "specmode_postboot.png")

	detector := func(cell int) uint32 {
		// clear ascending RAMP across frequency: a real trace = a diagonal line.
		return uint32(0x30 + cell)
	}
	paint, paint2 := 0, 0
	b0ecSeen := map[uint16]int{}
	for i := 0; i < 120000; i++ {
		for s := 0; s < 4; s++ {
			if m.CPU.Step() != nil {
				break
			}
			pc := m.CPU.Reg(cpu.PC)
			if pc >= 0x7d84 && pc < 0x7e40 {
				paint++
			}
			if pc >= 0x7cd6 && pc < 0x7d04 {
				paint2++
			}
		}
		m.CPU.Run(600)
		lb.Check(m.CPU.Reg(cpu.PC), m.CPU.SetReg)
		if i%5 == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Run(300)
			m.CPU.SetIRQ(0)
		}
		b0ecSeen[rdW(0xFFB0EC)]++
		a5 := m.CPU.Reg(cpu.A5)
		bf30 := rdL(0xFFBF30)
		bs := bf30 - 802
		if a5 >= bs && a5 < bf30 {
			m.Bus.Write(0xFFF200, bus.Word, detector(int(a5-bs)/2))
			m.CPU.SetIRQ(6)
			m.CPU.Run(250)
			m.CPU.SetIRQ(0)
		}
		if i == 3000 {
			fmt.Printf("  @i=3000: b0ec=%04X Lines=%d\n", rdW(0xFFB0EC), m.MMIO.Display.Lines)
			render(m, "specmode_early3k.png")
		}
		if i == 15000 {
			fmt.Printf("  @i=15000: b0ec=%04X Lines=%d\n", rdW(0xFFB0EC), m.MMIO.Display.Lines)
			render(m, "specmode_mid15k.png")
		}
	}
	fmt.Printf("DURING DRIVE: 0x7d84(paint) hits=%d  0x7cd6 hits=%d  Lines=%d\n", paint, paint2, m.MMIO.Display.Lines)
	fmt.Printf("  b0ec values seen: %v\n", b0ecSeen)
	render(m, "specmode_postdrive.png")
}
