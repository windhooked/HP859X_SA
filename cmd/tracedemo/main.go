// Command tracedemo renders the analog-model spectrum onto the firmware's real
// boot UI. The firmware's own trace-draw is DLP-blocked (docs/TRACE_DISPLAY_PATH.md),
// but the SweepEngine (pkg/emu/device) IS the data the firmware's sweep captures
// into the trace buffer. This overlays that 401-point spectrum on the firmware
// graticule so the modelled CAL peak + noise floor are visible — a visual check
// of the analog model end of the M2 trace path.
package main

import (
	"image/color"
	"image/png"
	"os"

	"github.com/windhooked/HP859X_SA/pkg/emu/device"
	"github.com/windhooked/HP859X_SA/pkg/emu/machine"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

func main() {
	rom, _ := romloader.LoadDir("hp8593a_eeproms")
	m, _ := machine.New8593A(rom)
	m.CPU.Reset()
	m.BootToOperating(165_000_000)
	img := m.MMIO.Display.RenderFrame()

	se := device.NewSweepEngine()
	// graticule box (approx, from the firmware boot render): x in [6,398], y in [8,205]
	const x0, x1, y0, y1 = 6, 398, 8, 205
	trace := color.RGBA{0x30, 0xff, 0x40, 0xff} // bright green SA trace
	prevX, prevY := -1, -1
	for p := 0; p < se.Points; p++ {
		frac := (se.LevelAt(p) - (se.Detector.RefLevelDBm - 80)) / 80 // 0=bottom,1=top
		if frac < 0 {
			frac = 0
		}
		if frac > 1 {
			frac = 1
		}
		x := x0 + p*(x1-x0)/(se.Points-1)
		y := y1 - int(frac*float64(y1-y0))
		// draw a short vertical run to connect points
		if prevX >= 0 {
			steps := y - prevY
			if steps < 0 {
				steps = -steps
			}
			for s := 0; s <= steps; s++ {
				yy := prevY
				if y > prevY {
					yy = prevY + s
				} else {
					yy = prevY - s
				}
				img.Set(prevX, yy, trace)
			}
		}
		img.Set(x, y, trace)
		prevX, prevY = x, y
	}
	f, _ := os.Create("screens/trace_demo.png")
	png.Encode(f, img)
	f.Close()
	println("wrote screens/trace_demo.png (analog spectrum on firmware UI)")
}
