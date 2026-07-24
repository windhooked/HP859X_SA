package machine

import (
	"os"
	"testing"

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
	m.CPU.Reset()
	m.MMIO.SweepActive = true
	m.SweepDrive = true
	m.BootToOperatingWithSweep(250_000_000)
	chip := m.MMIO.Display.Chip
	chip.ClearColLogOn = true
	for i := 0; i < 60; i++ {
		m.bootLoop(2_000_000, nil)
	}
	log := chip.ClearColLog
	t.Logf("%d column ops logged", len(log))
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
