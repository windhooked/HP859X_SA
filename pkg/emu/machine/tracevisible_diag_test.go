package machine_test

import (
	"image"
	"image/png"
	"os"
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
)

// TestTraceVisibleDiag — prove the trace pipeline is complete (2026-07-14).
//
// The full capture→display→draw chain is decoded and EXECUTES with real data:
//
//	capture buffer 0x2FD508 (401 real samples) → fcn.cfbe reads+scales+stores →
//	display array [0x9546] → fcn.c7ac/ca5a draws a 401-pt APLL polyline.
//
// The ONE residual: the amplitude-scale factor at RAM 0xB1C2/0xB1C5 is left at
// the stale default 0x7FFF/0x7F (shift-by-127 zeroes every scaled point), because
// fcn.80a0 (the amplitude/ref-level scale setup) never computes it in the forced
// continuous-spectrum state — it is gated on ref-level/log-scale state (b204/b20a)
// the synthetic mode-entry doesn't establish. That is the amplitude analogue of
// the DAC→Hz residual: a state/calibration gap, not a control-flow gate.
//
// This test CONFIRMS the pipeline by forcing a sane scale shift (b1c5:=0) — a
// DIAGNOSTIC, not shipped — and rendering the persistence composite: with a sane
// scale the display array fills with the real spectrum (nonzero, varied) and the
// trace paints. It asserts the pipeline works (array becomes nonzero) and writes
// screens/trace_visible.png for visual confirmation.
func TestTraceVisibleDiag(t *testing.T) {
	if testing.Short() {
		t.Skip("boot-length diagnostic")
	}
	m := newMachine(t)
	m.MMIO.SweepActive = true
	m.BootToOperatingWithSweep(150_000_000)
	if !m.EnterContinuousSpectrum() {
		t.Fatal("EnterContinuousSpectrum failed")
	}
	m.CPU.SetReg(cpu.PC, 0x00018568)

	base := uint32(m.Bus.Read(0xFF9546, bus.Long))
	nzOf := func() int {
		n := 0
		for i := 0; i < 403; i++ {
			if m.Bus.Read(base+uint32(i*2), bus.Word) != 0 {
				n++
			}
		}
		return n
	}
	// Baseline: degenerate scale ⇒ display array flat/zero.
	for i := 0; i < 3000; i++ {
		m.CPU.Run(2000)
		if i%5 == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Run(400)
			m.CPU.SetIRQ(0)
		}
		m.DriveOneSweepChunk()
	}
	t.Logf("default scale (b1c2=%#x b1c5=%#x): display array %d/403 nonzero",
		m.Bus.Read(0xFFB1C2, bus.Word), m.Bus.Read(0xFFB1C5, bus.Byte), nzOf())

	// DIAGNOSTIC: force a sane amplitude-scale shift so cfbe's scale no longer
	// zeroes every point. This is NOT shipped — it confirms the pipeline.
	m.Bus.Write(0xFFB1C5, bus.Byte, 0)

	// Persistence composite over ~3 sweeps (the draw is incremental).
	m.MMIO.Display.Chip.SetRenderLive(true)
	var composite *image.RGBA
	litInterior := map[int]struct{}{}
	for i := 0; i < 4000; i++ {
		m.CPU.Run(2000)
		if i%5 == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Run(400)
			m.CPU.SetIRQ(0)
		}
		m.DriveOneSweepChunk()
		if i%20 != 0 {
			continue
		}
		fr := m.MMIO.Display.RenderFrame()
		if composite == nil {
			composite = image.NewRGBA(fr.Bounds())
		}
		b := fr.Bounds()
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				r, g, _, _ := fr.At(x, y).RGBA()
				if r>>8 > 0x40 || g>>8 > 0x40 {
					composite.Set(x, y, fr.At(x, y))
					if x >= 8 && x <= 400 && y >= 16 && y <= 200 {
						litInterior[y*1024+x] = struct{}{}
					}
				}
			}
		}
	}
	nz := nzOf()
	t.Logf("sane scale (b1c5:=0): display array %d/403 nonzero; composite interior lit=%d",
		nz, len(litInterior))
	if nz < 350 {
		t.Errorf("pipeline broken: display array only %d/403 nonzero after sane scale", nz)
	}
	if f, err := os.Create("../../../screens/trace_visible.png"); err == nil {
		png.Encode(f, composite)
		f.Close()
	}
}
