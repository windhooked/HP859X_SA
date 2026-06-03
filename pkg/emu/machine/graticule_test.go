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
	// Upper graph interior (display coords; DisplayWidth=544, DisplayHeight=384).
	const x0, x1 = 60, 430
	const y0, y1 = 45, 240
	height := y1 - y0

	// Per-column lit count; a "grid column" is lit over a good fraction of the
	// height (a vertical line, distinct from the wiggly trace which lives lower).
	gridCols := 0
	for x := x0; x < x1; x++ {
		lit := 0
		for y := y0; y < y1; y++ {
			if img.Pix[y*img.Stride+x*4] > 30 {
				lit++
			}
		}
		if lit >= height/3 {
			gridCols++
		}
	}

	if os.Getenv("SAVE") != "" {
		f, _ := os.Create("../../../screens/graticule_grid.png")
		png.Encode(f, img)
		f.Close()
	}

	t.Logf("vertical grid columns detected in graph interior: %d (need >=6)", gridCols)
	if gridCols < 6 {
		t.Errorf("graticule vertical grid not rendered: only %d lit columns in the graph interior "+
			"(the 0x4400 grid pattern is routed off-screen). Expected the dim vertical grid (>=6 columns).", gridCols)
	}
}
