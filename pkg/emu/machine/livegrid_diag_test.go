package machine

import (
	"os"
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

// Scratch: does the LIVE render lose the graticule grid vs the stable snapshot
// at arbitrary instants during the sweep cycle?
func TestLiveGridDiag(t *testing.T) {
	if os.Getenv("DIAG") == "" {
		t.Skip("DIAG=1")
	}
	rom, err := romloader.LoadDir("../../../hp8593a_eeproms")
	if err != nil {
		t.Skip("rom")
	}
	m, _ := New8593A(rom)
	m.CPU.Reset()
	m.MMIO.SweepActive = true
	m.SweepDrive = true
	m.BootToOperatingWithSweep(250_000_000)
	chip := m.MMIO.Display.Chip

	lit := func(img interface {
		At(x, y int) (r, g, b, a uint32)
	}) int {
		type rgba interface {
			At(x, y int) (r, g, b, a uint32)
		}
		_ = img
		return 0
	}
	_ = lit
	countLit := func(live bool) int {
		chip.SetRenderLive(live)
		img := chip.RenderScanout()
		n := 0
		for i := 0; i < len(img.Pix); i += 4 {
			if img.Pix[i] > 0 {
				n++
			}
		}
		return n
	}
	// Sample 8 arbitrary instants ~2M cycles apart (one GUI frame each) —
	// and for each frame, also run the CRT BEAM INTEGRATION (16 union samples
	// across the frame, as cmd/gui does) to show it restores the content a
	// single live point-sample misses.
	var union []uint8
	for s := 0; s < 8; s++ {
		for i := range union {
			union[i] = 0
		}
		for k := 0; k < 16; k++ {
			m.bootLoop(2_000_000/16, nil)
			union, _, _ = chip.ScanoutUnion(union)
		}
		unionN := 0
		for _, v := range union {
			if v != 0 {
				unionN++
			}
		}
		liveN := countLit(true)
		snapN := countLit(false)
		t.Logf("sample %d: live=%d lit  snapshot=%d lit  BEAM-UNION=%d lit", s, liveN, snapN, unionN)
		_ = liveN
	}
	// How many FRAMES of accumulated union does full content take? (Sets the
	// phosphor decay: the tail must bridge the repaint period.)
	var acc []uint8
	for f := 1; f <= 16; f++ {
		for k := 0; k < 16; k++ {
			m.bootLoop(2_000_000/16, nil)
			acc, _, _ = chip.ScanoutUnion(acc)
		}
		n := 0
		for _, v := range acc {
			if v != 0 {
				n++
			}
		}
		t.Logf("acc over %2d frames: %d lit", f, n)
	}
}
