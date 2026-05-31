// Command sweepengine drives a faithful BOUNDED sweep: IRQ6 sample-capture gated
// on A5<bf30 (so we never over-fire past buffer-full and creep A5 past the peak
// handler's post-increment bf30 check), detector indexed by trace-buffer cell,
// no IRQ1 (which perturbs menu state). The firmware's IRQ6 handler (peak mode at
// bf34=0x410A) advances A5 per point and re-arms via 0x40C2; fcn.CFBE draws each
// A5-$b1d8 delta. Renders to ./screens/.
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

	for done := 0; done < 160_000_000; done += 2000 {
		m.CPU.Run(2000)
		lb.Check(m.CPU.Reg(cpu.PC), m.CPU.SetReg)
		if (done/2000)%5 == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Run(400)
			m.CPU.SetIRQ(0)
		}
	}
	bufStart := rdL(0xFFBF30) - 802 // 401 words below the end pointer
	fmt.Printf("booted: Lines=%d A5=%08X bf30=%08X bufStart=%08X bf34=%08X\n",
		m.MMIO.Display.Lines, m.CPU.Reg(cpu.A5), rdL(0xFFBF30), bufStart, rdL(0xFFBF34))

	// detector: noise floor + a peak (indexed by trace-buffer cell 0..400).
	detector := func(cell int) uint32 {
		v := 0x40
		d := cell - 200
		if d < 0 {
			d = -d
		}
		if d < 25 {
			v += (25 - d) * 14
		}
		return uint32(v)
	}
	linesBefore := m.MMIO.Display.Lines
	sweeps := 0
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
		// Only capture while the firmware's sweep window is open (A5 inside buffer).
		if a5 >= bs && a5 < bf30 {
			cell := int(a5-bs) / 2
			m.Bus.Write(0xFFF200, bus.Word, detector(cell))
			m.CPU.SetIRQ(6)
			m.CPU.Run(250)
			m.CPU.SetIRQ(0)
			if cell == 0 {
				sweeps++
			}
		}
		if i%20000 == 0 && i > 0 {
			fmt.Printf("  i=%d Lines=%d A5=%08X bf30=%08X sweeps~%d\n",
				i, m.MMIO.Display.Lines, m.CPU.Reg(cpu.A5), rdL(0xFFBF30), sweeps)
		}
		if m.MMIO.Display.Lines > linesBefore+200 {
			fmt.Printf("** trace drawing at i=%d: Lines %d -> %d\n", i, linesBefore, m.MMIO.Display.Lines)
			break
		}
	}
	fmt.Printf("final: Lines %d -> %d (drew=%v) sweeps~%d A5=%08X\n",
		linesBefore, m.MMIO.Display.Lines, m.MMIO.Display.Lines > linesBefore+200, sweeps, m.CPU.Reg(cpu.A5))
	if f, err := os.Create("screens/sweepengine.png"); err == nil {
		png.Encode(f, m.MMIO.Display.RenderFrame())
		f.Close()
		fmt.Println("wrote screens/sweepengine.png")
	}
}
