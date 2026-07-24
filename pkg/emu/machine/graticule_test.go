package machine

import (
	"image/png"
	"os"
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

// TestGraticuleGridVisible pins the FINDING (docs/HD63484_DRAWING_COMMANDS.md)
// that the graticule is the firmware's 10×8 VECTOR grid — explicit ALINE/RLINE
// division lines + an ARCT box — repainted EVERY display cycle (verified: 9
// vertical + 7 horizontal long axis-aligned lines, ×11 per capture). It is NOT
// the 0x4400 raster dither (an earlier, now-disproven theory). Under clean-clear
// the grid survives because the stable-frame snapshot captures the buffer right
// after the grid repaint. This test asserts the grid renders in the visible graph
// — several regularly-spaced vertical columns in the upper graph interior (above
// the trace band), in the register-derived scanout.
func TestGraticuleGridVisible(t *testing.T) {
	rom, err := romloader.LoadDir("../../../hp8593a_eeproms")
	if err != nil {
		t.Skip("rom not available")
	}
	m, _ := New8593A(rom)
	m.CPU.Reset()
	m.MMIO.SweepActive = true
	m.SweepDrive = true
	m.BootToOperatingWithSweep(250_000_000)
	m.BootToOperatingWithSweep(10_000_000)

	// Plain amber scanout (foreground R≈0xFF) for pixel assertions; the saved
	// inspection image uses the by-command view.
	img := m.MMIO.Display.Chip.RenderScanout()
	w := img.Bounds().Dx()
	// Graph interior (scanout coords): right of the left label column, left of
	// the softkey column, above the trace noise band at the bottom. The 2bpp
	// scanout is 512 px wide (8 px/word × MWR1=64); the graph spans ≈ x 40..448.
	x0, x1 := 50, 440
	if x1 > w {
		x1 = w
	}
	y0, y1 := 25, 185

	// Count HORIZONTAL grid lines: the firmware's amplitude divisions span the full
	// graph width, so a grid row lights a large fraction of x0..x1; empty graph rows
	// light ~nothing (clean-clear leaves no dither residue). The 8-division grid
	// gives ~7 interior horizontals + the box top edge. (The vertical divisions are
	// too finely dotted to threshold per-column, so we assert on the horizontals.)
	span := x1 - x0
	gridRows := 0
	for y := y0; y < y1; y++ {
		lit := 0
		for x := x0; x < x1; x++ {
			if img.Pix[y*img.Stride+x*4] > 30 {
				lit++
			}
		}
		// The divisions are DOTTED (dots every ~4 px at 2bpp) — a grid row
		// lights ≥ ~20% of the span; empty rows light ~nothing.
		if lit >= span/5 {
			gridRows++
		}
	}

	if os.Getenv("SAVE") != "" {
		f, _ := os.Create("../../../screens/graticule_grid.png")
		png.Encode(f, m.MMIO.Display.Chip.RenderScanoutByCmd())
		f.Close()
	}

	t.Logf("graticule horizontal grid lines in graph interior: %d", gridRows)
	// The 8-division grid has ~7 interior horizontals + the box top edge. Require
	// several (the grid is present), well above the 1 a bare box top would give.
	if gridRows < 5 {
		t.Errorf("graticule grid not rendered (%d horizontal lines) — the firmware's "+
			"10×8 ALINE grid should be repainted and visible each cycle", gridRows)
	}
}
