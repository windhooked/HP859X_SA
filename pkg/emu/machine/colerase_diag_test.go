package machine

import (
	"fmt"
	"os"
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"

	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

// Scratch: do the firmware's per-column SCLR AND erases sweep across the whole
// graph (all x), and with what mask?
func TestColEraseDiag(t *testing.T) {
	if os.Getenv("DIAG") == "" {
		t.Skip("DIAG=1")
	}
	rom, err := romloader.LoadDir("../../../hp8593a_eeproms")
	if err != nil {
		t.Skip("rom")
	}
	m, _ := New8593A(rom)
	if v := os.Getenv("PACE"); v != "" {
		fmt.Sscanf(v, "%d", &m.SweepCyclesPerPoint)
	}
	m.CPU.Reset()
	m.MMIO.SweepActive = true
	m.SweepDrive = true
	m.BootToOperatingWithSweep(250_000_000)
	chip := m.MMIO.Display.Chip
	chip.ClearColLogOn = true
	chip.ClearColExt = func() uint32 { return m.CPU.Reg(cpu.A5) }
	chip.APLLColorHist = map[uint16]int{}
	t.Logf("A9A2 (samples/point) = %d", m.Bus.Read(0xFFA9A2, 2))
	for i := 0; i < 60; i++ {
		m.bootLoop(2_000_000, nil)
	}
	t.Logf("APLL draws by CL1: %v", chip.APLLColorHist)
	log := chip.ClearColLog
	t.Logf("%d column ops logged", len(log))
	// Correlate bar column vs A5 sample index for the first 40 ops.
	for i, e := range log {
		if i >= 40 {
			break
		}
		col := int(e[0]) - 0x3a45
		a5 := e[1]
		idx := -1
		if a5 >= 0x2FD508 && a5 < 0x2FD90C {
			idx = int(a5-0x2FD508) / 2
		}
		t.Logf("  op%02d: barcol=%2d  A5idx=%3d  (px %d vs %d)", i, col, idx, col*8, idx)
	}
	// Column coverage: distinct RWP words + mask histogram.
	words := map[uint32]int{}
	masks := map[uint32]int{}
	pats := map[uint32]int{}
	for _, e := range log {
		words[e[0]]++
		masks[e[1]]++
		pats[e[2]]++
	}
	t.Logf("distinct RWP words: %d  (per-word ~%d hits)", len(words), len(log)/max(1, len(words)))
	t.Logf("masks: %v", masks)
	t.Logf("patterns: %v", pats)
	// Show min/max word to see the x-range covered.
	var lo, hi uint32 = 0xFFFFFFFF, 0
	for w := range words {
		if w < lo {
			lo = w
		}
		if w > hi {
			hi = w
		}
	}
	t.Logf("RWP word range: %#x .. %#x (span %d words = %d px cols)", lo, hi, hi-lo, (hi-lo)*16)
}
