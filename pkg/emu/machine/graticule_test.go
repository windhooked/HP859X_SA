package machine

import (
	"image/png"
	"os"
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

// TestGraticuleGridVisible pins the FINDING that the graticule's vertical grid
// is the firmware's 0x4400 raster PATTERN fill (not vector lines — a full-sweep
// geometry scan finds zero grid polylines). The firmware fills that pattern into
// the back page (MAR=0x4000), and the display is meant to show it; our model
// currently routes it OFF-SCREEN (bgVram rows 256+), so the graph interior is
// blank. This test asserts the grid is actually rendered IN the visible graph —
// i.e. the graph interior contains a set of regularly-lit vertical columns (the
// grid lines), not just the trace.
//
// Region: the UPPER graph interior, above the trace band and left of the softkey
// column, so a pass means "grid present" rather than "trace present".
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

	img := m.MMIO.Display.Chip.RenderFrame()
	// Graph span incl. the box frame edges (graph left edge ≈ display x48, right
	// ≈ x448). Display coords; DisplayWidth=544, DisplayHeight=384.
	const x0, x1 = 40, 460
	const y0, y1 = 45, 240
	height := y1 - y0

	// Per-column count of near-full-height vertical lines. The accurate graticule is
	// the firmware's VECTOR frame (box left/right edges + a few dotted divisions) —
	// NOT the dense 0x4400 dither (suppressed by decision; it time-averages to a
	// faint CRT glow, and as hard stripes it was inaccurate). So we expect a SMALL
	// number of frame columns, not the old ~25 dither bars.
	frameCols := 0
	for x := x0; x < x1; x++ {
		lit := 0
		for y := y0; y < y1; y++ {
			if img.Pix[y*img.Stride+x*4] > 30 {
				lit++
			}
		}
		if lit >= height/2 {
			frameCols++
		}
	}

	if os.Getenv("SAVE") != "" {
		f, _ := os.Create("../../../screens/graticule_grid.png")
		png.Encode(f, img)
		f.Close()
	}

	t.Logf("near-full-height graticule frame columns in graph interior: %d", frameCols)
	// At least the box left edge must render (frame present); and we must NOT see the
	// dense dither back (which produced ~25 such columns).
	if frameCols < 1 {
		t.Errorf("graticule frame not rendered: no near-full-height vertical line in the graph interior")
	}
	if frameCols > 10 {
		t.Errorf("dense dither appears to be back: %d near-full-height columns (expected the vector frame, a handful)", frameCols)
	}
}
