// Command annunctest: clear the annunciator source-status flags + packed words
// continuously DURING boot (before fcn.11B9A aggregates and the render draws),
// to test whether the graticule annunciators are then never drawn — the
// decisive end-to-end test of the cracked pipeline + a clean-boot mechanism.
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
	clr := []uint32{0xFFB1F0, 0xFFB1F6, 0xFFB1FA, 0xFFB1F8, 0xFFB1E0,
		0xFFB060, 0xFFB062, 0xFFB064, 0xFFB066, 0xFFB068,
		0xFFB08C, 0xFFB08E, 0xFFB090, 0xFFB092,
		0xFFB098, 0xFFB09A, 0xFFB09C, 0xFFB09E, 0xFFB0C2}
	for c := 0; c < 165_000_000; c += 2000 {
		for _, a := range clr {
			m.Bus.Write(a, bus.Word, 0)
		}
		m.CPU.Run(2000)
		lb.Check(m.CPU.Reg(cpu.PC), m.CPU.SetReg)
		if (c/2000)%5 == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Run(400)
			m.CPU.SetIRQ(0)
		}
	}
	out := "screens/annunc_boot_cleared.png"
	f, _ := os.Create(out)
	png.Encode(f, m.MMIO.Display.RenderFrame())
	f.Close()
	fmt.Printf("wrote %s\n", out)
}
