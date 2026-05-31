// Command tracedemo renders the analog-model spectrum on the firmware's real
// boot UI, parameterised by tuning (center freq, span, ref level, injected
// signal). It demonstrates the M3 analog-model capability — the trace follows
// CF/span/RBW and shows injected signals at their true frequency/level — even
// though the firmware's own trace-draw is still DLP-blocked. Each run boots
// once and renders the requested tuning(s) onto the firmware graticule.
//
//	go run ./cmd/tracedemo                       # default full-span (CAL on left)
//	go run ./cmd/tracedemo -cf 300e6 -span 10e6  # zoom to the 300 MHz CAL
//	go run ./cmd/tracedemo -cf 1e9 -span 2e9 -sig 1.2e9:-30  # injected tone
package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"

	"github.com/windhooked/HP859X_SA/pkg/emu/analog"
	"github.com/windhooked/HP859X_SA/pkg/emu/device"
	"github.com/windhooked/HP859X_SA/pkg/emu/machine"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

func main() {
	cf := flag.Float64("cf", 1.45e9, "center frequency Hz")
	span := flag.Float64("span", 2.9e9, "span Hz")
	refl := flag.Float64("refl", 0, "reference level dBm (top)")
	sigHz := flag.Float64("sigHz", 0, "injected signal frequency Hz (0=none)")
	sigDBm := flag.Float64("sigDBm", -30, "injected signal level dBm")
	rbw := flag.Float64("rbw", 1e6, "resolution bandwidth Hz")
	out := flag.String("out", "screens/trace_demo.png", "output PNG")
	flag.Parse()

	rom, _ := romloader.LoadDir("hp8593a_eeproms")
	m, _ := machine.New8593A(rom)
	m.CPU.Reset()
	m.BootToOperating(165_000_000)
	img := m.MMIO.Display.RenderFrame()

	se := device.NewSweepEngine()
	se.StartHz = *cf - *span/2
	if se.StartHz < 0 {
		se.StartHz = 0
	}
	se.StopHz = *cf + *span/2
	se.Detector.RefLevelDBm = *refl
	se.Spectrum.RBWHz = *rbw
	if *sigHz > 0 {
		se.Spectrum.Signals = []analog.Signal{{Hz: *sigHz, DBm: *sigDBm}}
	}

	drawTrace(img, se)
	f, _ := os.Create(*out)
	png.Encode(f, img)
	f.Close()
	fmt.Printf("wrote %s  (start=%.0fMHz stop=%.0fMHz ref=%.0fdBm)\n", *out, se.StartHz/1e6, se.StopHz/1e6, *refl)
}

func drawTrace(img *image.RGBA, se *device.SweepEngine) {
	const x0, x1, y0, y1 = 6, 398, 8, 205
	trace := color.RGBA{0x30, 0xff, 0x40, 0xff}
	prevX, prevY := -1, -1
	for p := 0; p < se.Points; p++ {
		frac := (se.LevelAt(p) - (se.Detector.RefLevelDBm - 80)) / 80
		if frac < 0 {
			frac = 0
		}
		if frac > 1 {
			frac = 1
		}
		x := x0 + p*(x1-x0)/(se.Points-1)
		y := y1 - int(frac*float64(y1-y0))
		if prevX >= 0 {
			lo, hi := prevY, y
			if lo > hi {
				lo, hi = hi, lo
			}
			for yy := lo; yy <= hi; yy++ {
				img.Set(prevX, yy, trace)
			}
		}
		img.Set(x, y, trace)
		prevX, prevY = x, y
	}
}
