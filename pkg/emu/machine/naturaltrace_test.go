package machine_test

import (
	"image/png"
	"os"
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
)

// TestNaturalTracePaints — the CORRECT trace-paints behaviour (2026-07-15).
//
// The sweep-driven boot ALREADY draws a real, amplitude-correct spectrum trace,
// with no forced state: the firmware's amplitude-scale setup fcn.80a0 runs
// during boot and computes a valid scale (b1c2 non-sentinel), the measurement
// processor fcn.cfbe fills the display trace array [0x9546] from the swept
// capture buffer, and fcn.c7ac/c992 draws the 401-point polyline — continuously
// redrawn each sweep. (This corrects the earlier "amplitude residual" finding,
// which was an artifact of the forced Machine.EnterContinuousSpectrum path —
// see docs/MEASURE_MODE_HANDOFF.md; that forced entry RESETS the scale to the
// degenerate sentinel and zeroes the display array.)
func TestNaturalTracePaints(t *testing.T) {
	if testing.Short() {
		t.Skip("boot-length")
	}
	m := newMachine(t)
	m.MMIO.SweepActive = true
	m.SweepDrive = true
	m.BootToOperatingWithSweep(200_000_000)

	rd := func(a uint32) uint32 { return m.Bus.Read(a, bus.Word) }
	base := uint32(m.Bus.Read(0xFF9546, bus.Long)) // display trace array
	dispNZ := func() int {
		n := 0
		for i := 0; i < 403; i++ {
			if rd(base+uint32(i*2)) != 0 {
				n++
			}
		}
		return n
	}

	// The amplitude scale must be a real value (not the 0x7FFF sentinel), and
	// the display trace array must be filled from the swept capture.
	b1c2 := rd(0xFFB1C2)
	if b1c2 == 0x7FFF || b1c2 == 0 {
		t.Errorf("amplitude scale b1c2=%#x — fcn.80a0 did not compute a real scale", b1c2)
	}
	if nz := dispNZ(); nz < 350 {
		t.Errorf("display trace array only %d/403 nonzero — trace not filled", nz)
	}

	// The trace redraws continuously: count polyline lines over a few sweeps.
	chip := m.MMIO.Display.Chip
	chip.EnableLineLog()
	for phase := 0; phase < 3; phase++ {
		l0 := len(chip.LineLog)
		for i := 0; i < 8000; i++ {
			m.CPU.Run(2000)
			if i%5 == 0 {
				m.CPU.SetIRQ(5)
				m.CPU.Run(400)
				m.CPU.SetIRQ(0)
			}
			m.DriveOneSweepChunk()
		}
		if len(chip.LineLog)-l0 < 400 {
			t.Errorf("phase %d: only %d trace lines drawn — trace not redrawing", phase, len(chip.LineLog)-l0)
		}
	}
	t.Logf("natural trace: b1c2=%#x b1c5=%#x dispNZ=%d — real amplitude-correct spectrum",
		b1c2, m.Bus.Read(0xFFB1C5, bus.Byte), dispNZ())

	chip.SetRenderLive(true)
	if f, err := os.Create("../../../screens/trace_natural.png"); err == nil {
		png.Encode(f, chip.RenderScanout())
		f.Close()
	}
}
