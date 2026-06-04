package machine

import (
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

// TestTraceSolid guards that the spectrum trace renders SOLID. The trace is an
// APLL polyline whose solidity comes from the firmware's line stipple (0xFFFF set
// before it), not from a force-solid override in the polyline handler (removed so
// the dashed hp-logo "p" RPLL honours its 0xCCCC stipple). If a regression let the
// trace inherit a dashed stipple, its CAL-peak spike would break into short
// dotted segments. We assert a long contiguous VERTICAL lit run exists in the
// graph (the solid CAL-peak spike); a dashed trace yields only ~2-px runs.
func TestTraceSolid(t *testing.T) {
	rom, err := romloader.LoadDir("../../../hp8593a_eeproms")
	if err != nil {
		t.Skip("rom not available")
	}
	m, _ := New8593A(rom)
	m.CPU.Reset()
	m.MMIO.SweepActive = true
	m.SweepDrive = true
	m.BootToOperatingWithSweep(250_000_000)
	m.BootToOperatingWithSweep(40_000_000)

	img := m.MMIO.Display.Chip.RenderScanout() // amber; foreground R≈0xFF
	w := img.Bounds().Dx()
	x0, x1 := 100, 470
	if x1 > w {
		x1 = w
	}
	y0, y1 := 30, 240
	best := 0
	for x := x0; x < x1; x++ {
		run := 0
		for y := y0; y < y1; y++ {
			if img.Pix[y*img.Stride+x*4] > 30 { // red channel lit
				run++
				if run > best {
					best = run
				}
			} else {
				run = 0
			}
		}
	}
	t.Logf("longest contiguous vertical trace run: %d px", best)
	if best < 25 {
		t.Errorf("no solid vertical trace feature (longest run %d px) — the trace appears dashed; "+
			"the firmware's 0xFFFF stipple may not be active at trace draw", best)
	}
}
