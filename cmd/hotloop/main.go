// IRQ1+IRQ6 two-interrupt sweep experiment: drive IRQ1 (sweep step) and IRQ6
// (sample capture) together during boot, paced, so the sweep ramps across the
// band AND captures. Render + classify drawn lines (clean spectrum vs vertical bars).
package main

import (
	"fmt"
	"image/png"
	"os"

	"github.com/windhooked/HP859X_SA/internal/emutest"
	"github.com/windhooked/HP859X_SA/pkg/emu/analog"
	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
	"github.com/windhooked/HP859X_SA/pkg/emu/machine"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

func main() {
	mode := "irq1+irq6"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}
	rom, _ := romloader.LoadDir("hp8593a_eeproms")
	m, _ := machine.New8593A(rom)
	m.CPU.Reset()
	m.MMIO.SweepActive = true
	m.MMIO.Sweep.Spectrum.Signals = []analog.Signal{{Hz: 1.45e9, DBm: -30}, {Hz: 0.9e9, DBm: -45}, {Hz: 2.1e9, DBm: -55}}
	lb := emutest.NewLoopBreaker(50)
	rdL := func(a uint32) uint32 { return m.Bus.Read(a, bus.Long) }
	rdW := func(a uint32) uint16 { return uint16(m.Bus.Read(a, bus.Word)) }
	fireIRQ := func(level, cyc int) { m.CPU.SetIRQ(level); m.CPU.Run(cyc); m.CPU.SetIRQ(0) }

	const total = 250_000_000
	for done := 0; done < total; done += 2000 {
		m.CPU.Run(2000)
		lb.Check(m.CPU.Reg(cpu.PC), m.CPU.SetReg)
		if (done/2000)%5 == 0 {
			fireIRQ(5, 400)
		}
		bf34 := rdL(0xFFBF34)
		if bf34 == 0x40B8 || bf34 == 0x410A {
			if mode == "irq1+irq6" || mode == "irq1" {
				fireIRQ(1, 400) // sweep step: advance ramp, reprogram DACs
			}
			if mode == "irq1+irq6" || mode == "irq6" {
				for k := 0; k < 8 && m.CPU.Reg(cpu.A5) < rdL(0xFFBF30); k++ {
					fireIRQ(6, 250) // capture sample
				}
			}
		}
	}
	// final render + LineLog over one more driven window
	m.MMIO.Display.Chip.EnableLineLog()
	for done := 0; done < 8_000_000; done += 2000 {
		m.CPU.Run(2000)
		lb.Check(m.CPU.Reg(cpu.PC), m.CPU.SetReg)
		if (done/2000)%5 == 0 {
			fireIRQ(5, 400)
		}
		bf34 := rdL(0xFFBF34)
		if bf34 == 0x40B8 || bf34 == 0x410A {
			if mode != "irq6" {
				fireIRQ(1, 400)
			}
			if mode != "irq1" {
				for k := 0; k < 8 && m.CPU.Reg(cpu.A5) < rdL(0xFFBF30); k++ {
					fireIRQ(6, 250)
				}
			}
		}
	}
	ll := m.MMIO.Display.Chip.LineLog
	v, h, d := 0, 0, 0
	for _, l := range ll {
		switch {
		case l.X0 == l.X1:
			v++
		case l.Y0 == l.Y1:
			h++
		default:
			d++
		}
	}
	fmt.Printf("mode=%s  PC=%06X befa=%04x lines(total)=%d  window-lines=%d (V=%d H=%d D=%d)\n",
		mode, m.CPU.Reg(cpu.PC), rdW(0xFFBEFA), m.MMIO.Display.Chip.Lines, len(ll), v, h, d)
	// dump trace buffer (0x2FD508 onward) to see if it holds a spectrum or flat
	// scan whole buffer region for any non-zero (where does capture land?)
	nz, firstNZ := 0, uint32(0)
	for a := uint32(0x2FC000); a < 0x300000; a += 2 {
		if rdW(a) != 0 {
			nz++
			if firstNZ == 0 {
				firstNZ = a
			}
		}
	}
	fmt.Printf("non-zero words in 0x2FC000-0x300000: %d  first@%06X\n", nz, firstNZ)
	fmt.Printf("A5=%06X bf30=%06X bf3e=%04x befa=%04x\n", m.CPU.Reg(cpu.A5), rdL(0xFFBF30), rdW(0xFFBF3E), rdW(0xFFBEFA))
	fmt.Printf("trace buffer @0x2FD508 (20 words): ")
	for k := uint32(0); k < 20; k++ {
		fmt.Printf("%03x ", rdW(0x2FD508+k*2))
	}
	fmt.Println()
	fmt.Printf("SweepEngine sample probe (10 DetectADC): ")
	for k := 0; k < 10; k++ {
		fmt.Printf("%03x ", m.MMIO.Sweep.DetectADC())
	}
	fmt.Println()
	out := "screens/exp_" + mode + ".png"
	img := m.MMIO.Display.Chip.RenderFrame()
	f, _ := os.Create(out)
	png.Encode(f, img)
	f.Close()
	fmt.Println("wrote", out)
}
