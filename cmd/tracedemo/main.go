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
//	go run ./cmd/tracedemo -live                 # LIVE: window centred on the
//	                                             # firmware's real YTO coil DACs
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
	live := flag.Bool("live", false, "render the machine's own SweepEngine with the window centred on the firmware's real YTO coil DACs (★ 2026-07-12 A7 map) instead of a CLI-parameterised span")
	out := flag.String("out", "screens/trace_demo.png", "output PNG")
	flag.Parse()

	rom, _ := romloader.LoadDir("hp8593a_eeproms")
	m, _ := machine.New8593A(rom)
	m.CPU.Reset()

	var se *device.SweepEngine
	if *live {
		// Sweep-driven boot: syncSweepTune feeds the live coil DACs into the
		// machine's own SweepEngine, so its window centres on the firmware's
		// actual YTO tuning. Render THAT engine (not a fresh parameterised one).
		m.MMIO.SweepActive = true
		m.BootToOperatingWithSweep(200_000_000)
		se = m.MMIO.Sweep
		se.Detector.RefLevelDBm = *refl
		se.Spectrum.RBWHz = *rbw
		if *sigHz > 0 {
			se.SetSignals([]analog.Signal{{Hz: *sigHz, DBm: *sigDBm}})
		}
		fm, fine, coarse := m.MMIO.YTOCoilDACs()
		fmt.Printf("live coil DACs: FM=%#04x fine=%#04x coarse=%#04x → centre %.3f GHz (TuneActive=%v)\n",
			fm, fine, coarse, se.Tune.TunedHz()/1e9, se.TuneActive)
	} else {
		m.BootToOperating(165_000_000)
		se = device.NewSweepEngine()
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
	}
	img := m.MMIO.Display.RenderFrame()

	drawTrace(img, se)
	f, _ := os.Create(*out)
	png.Encode(f, img)
	f.Close()
	start, stop := se.Window()
	fmt.Printf("wrote %s  (start=%.0fMHz stop=%.0fMHz ref=%.0fdBm)\n", *out, start/1e6, stop/1e6, *refl)
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
