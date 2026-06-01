// sweeprun2 demonstrates Machine.BootToOperatingWithSweep: boots the instrument
// with the analog sweep driven (IRQ1+IRQ6), injects a few test tones, and renders
// the live screen with a drawn trace to screens/sweeprun2.png.
package main

import (
	"fmt"
	"image/png"
	"os"

	"github.com/windhooked/HP859X_SA/pkg/emu/analog"
	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/machine"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

func main() {
	rom, _ := romloader.LoadDir("hp8593a_eeproms")
	m, _ := machine.New8593A(rom)
	m.CPU.Reset()
	// in-band tones + one out-of-band (5 GHz, outside the 0..2.9 GHz span) to show
	// the boundary check drop, and one over-range amplitude (+20 dBm) to show clamp.
	dropped := m.MMIO.Sweep.SetSignals([]analog.Signal{
		{Hz: 1.45e9, DBm: -30}, {Hz: 0.9e9, DBm: -45}, {Hz: 2.1e9, DBm: 20},
		{Hz: 5.0e9, DBm: -25},
	})
	fmt.Printf("SetSignals dropped %d out-of-band signal(s)\n", dropped)
	m.BootToOperatingWithSweep(250_000_000)

	rdL := func(a uint32) uint32 { return m.Bus.Read(a, bus.Long) }
	rdW := func(a uint32) uint16 { return uint16(m.Bus.Read(a, bus.Word)) }
	nz := 0
	for a := uint32(0x2FD508); a < 0x2FD82A; a += 2 {
		if rdW(a) != 0 {
			nz++
		}
	}
	fmt.Printf("A5=%06X bf30=%06X befa=%04x  trace-buffer non-zero pts=%d  lines=%d glyphs=%d\n",
		m.CPU.Reg(0)&0, rdL(0xFFBF30), rdW(0xFFBEFA), nz, m.MMIO.Display.Chip.Lines, m.MMIO.Display.Chip.Glyphs)
	img := m.MMIO.Display.Chip.RenderFrame()
	f, _ := os.Create("screens/sweeprun2.png")
	png.Encode(f, img)
	f.Close()
	fmt.Println("wrote screens/sweeprun2.png")
}
