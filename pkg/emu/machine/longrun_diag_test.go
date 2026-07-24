package machine

import (
	"image/png"
	"os"
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

// Scratch: long natural run with faithful SCLR (CleanClear=false) vs clean
// clear — does the faithful dither render the dotted graticule grid (the
// trace_longrun.png reference) without leaving a permanent trace forest?
func TestLongrunGridDiag(t *testing.T) {
	if os.Getenv("DIAG") == "" {
		t.Skip("DIAG=1")
	}
	rom, err := romloader.LoadDir("../../../hp8593a_eeproms")
	if err != nil {
		t.Skip("rom")
	}
	for _, clean := range []bool{true, false} {
		m, _ := New8593A(rom)
		m.MMIO.Display.Chip.CleanClear = clean
		m.CPU.Reset()
		m.MMIO.SweepActive = true
		m.SweepDrive = true
		m.BootToOperatingWithSweep(250_000_000)
		// Long natural run: many sweep cycles.
		for i := 0; i < 150; i++ {
			m.bootLoop(2_000_000, nil)
		}
		chip := m.MMIO.Display.Chip
		chip.SetRenderLive(false) // stable snapshot for a complete frame
		img := chip.RenderScanout()
		lit := 0
		for i := 0; i < len(img.Pix); i += 4 {
			if img.Pix[i] > 0 {
				lit++
			}
		}
		name := "../../../screens/longrun_clean.png"
		if !clean {
			name = "../../../screens/longrun_faithful.png"
		}
		if f, err := os.Create(name); err == nil {
			png.Encode(f, img)
			f.Close()
		}
		t.Logf("CleanClear=%v: %d lit pixels -> %s", clean, lit, name)
	}
}
