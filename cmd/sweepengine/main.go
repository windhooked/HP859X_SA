// Drives the sweep (detector-ADC model) and captures what the firmware actually
// DRAWS: the draw-command deltas (Lines/Dots/Moves/Glyphs) and the drawLine
// segments, classified into the graticule trace area vs UI. Tells us whether the
// trace is painted (as vectors/dots) or the paint no-ops. Renders to ./screens/.
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
	d := m.MMIO.Display
	c0 := [4]int{d.Lines, d.Dots, d.Moves, d.Glyphs}
	m.MMIO.SweepActive = true
	m.MMIO.SweepPoints = 401
	m.MMIO.Display.EnableLineLog()
	m.MMIO.Display.EnableDotLog()
	prevA5 := m.CPU.Reg(cpu.A5)
	for i := 0; i < 80000; i++ {
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
		if a5 < prevA5 {
			m.MMIO.ResetSweep()
		}
		prevA5 = a5
		if a5 >= bs && a5 < bf30 {
			m.CPU.SetIRQ(6)
			m.CPU.Run(250)
			m.CPU.SetIRQ(0)
		}
	}
	c1 := [4]int{d.Lines, d.Dots, d.Moves, d.Glyphs}
	nm := [4]string{"Lines", "Dots", "Moves", "Glyphs"}
	for i := range nm {
		fmt.Printf("  %-7s +%d\n", nm[i], c1[i]-c0[i])
	}
	// classify captured drawLine segments: trace area is the lower graticule.
	ll := m.MMIO.Display.LineLog
	tr, ui := 0, 0
	var sample []string
	for _, r := range ll {
		short := abs(r.X1-r.X0) < 12 && abs(r.Y1-r.Y0) < 40
		inGrat := r.X0 > 5 && r.X0 < 700 && r.Y0 > 5 && r.Y0 < 460
		if short && inGrat {
			tr++
			if len(sample) < 12 {
				sample = append(sample, fmt.Sprintf("(%d,%d)->(%d,%d)", r.X0, r.Y0, r.X1, r.Y1))
			}
		} else {
			ui++
		}
	}
	fmt.Printf("drawLine during sweep: %d total; %d short/in-area (trace-like), %d other\n", len(ll), tr, ui)
	fmt.Printf("  sample trace-like segments: %v\n", sample)
	dl := m.MMIO.Display.DotLog
	minx, miny, maxx, maxy := 1<<30, 1<<30, -1, -1
	for _, p := range dl {
		if p.X < minx {
			minx = p.X
		}
		if p.Y < miny {
			miny = p.Y
		}
		if p.X > maxx {
			maxx = p.X
		}
		if p.Y > maxy {
			maxy = p.Y
		}
	}
	fmt.Printf("DOT positions: %d dots, bbox x[%d..%d] y[%d..%d]\n", len(dl), minx, maxx, miny, maxy)
	if len(dl) > 0 {
		fmt.Print("  first 16: ")
		for i, p := range dl {
			if i >= 16 {
				break
			}
			fmt.Printf("(%d,%d) ", p.X, p.Y)
		}
		fmt.Println()
		fmt.Print("  last 16:  ")
		for i := len(dl) - 16; i < len(dl); i++ {
			if i >= 0 {
				fmt.Printf("(%d,%d) ", dl[i].X, dl[i].Y)
			}
		}
		fmt.Println()
	}
	if f, err := os.Create("screens/sweepengine.png"); err == nil {
		png.Encode(f, m.MMIO.Display.RenderFrame())
		f.Close()
		fmt.Println("wrote screens/sweepengine.png")
	}
}
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
