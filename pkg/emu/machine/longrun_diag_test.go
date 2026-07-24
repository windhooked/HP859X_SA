package machine

import (
	"fmt"
	"image/png"
	"os"
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

// Scratch: long natural run with faithful SCLR (CleanClear=false) vs clean
// clear — does the faithful dither render the dotted graticule grid (the
// trace_longrun.png reference) without leaving a permanent trace forest?
func popcount(w uint16) int {
	n := 0
	for ; w != 0; w &= w - 1 {
		n++
	}
	return n
}

func TestLongrunGridDiag(t *testing.T) {
	if os.Getenv("DIAG") == "" {
		t.Skip("DIAG=1")
	}
	rom, err := romloader.LoadDir("../../../hp8593a_eeproms")
	if err != nil {
		t.Skip("rom")
	}
	pace := 0
	if v := os.Getenv("PACE"); v != "" {
		fmt.Sscanf(v, "%d", &pace)
	}
	for _, clean := range []bool{true, false} {
		m, _ := New8593A(rom)
		if pace > 0 {
			m.SweepCyclesPerPoint = pace
		}
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
		chip.SetRenderLive(true) // LIVE buffer (the snapshot may be stale)
		img := chip.RenderScanout()
		lit := 0
		for i := 0; i < len(img.Pix); i += 4 {
			if img.Pix[i] > 0 {
				lit++
			}
		}
		// Phase census of the graph region (rows 24..233, word-cols 5..30):
		// odd-bit pixels = the AAAA draw phase (erased by AND-5555);
		// even-bit = the 5555 phase (protected). Persistent even-phase trace
		// pixels imply an UNGATED draw path.
		var oddN, evenN int
		for row := 24; row <= 233; row++ {
			for wc := 5; wc <= 30; wc++ {
				w := chip.CoreWord(uint32(row*64 + wc))
				oddN += popcount(w & 0xAAAA)
				evenN += popcount(w & 0x5555)
			}
		}
		t.Logf("  graph-region phase census: odd(AAAA-phase)=%d even(5555-phase)=%d", oddN, evenN)
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
