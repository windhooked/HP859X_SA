package machine

import (
	"os"
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

// Scratch: which logical SCLRs does the BOOT emit that we skip (no-area-def
// no-op branch)? The boot->operating transition clear may be among them.
func TestBootSkipDiag(t *testing.T) {
	if os.Getenv("DIAG") == "" {
		t.Skip("DIAG=1")
	}
	rom, err := romloader.LoadDir("../../../hp8593a_eeproms")
	if err != nil {
		t.Skip("rom")
	}
	m, _ := New8593A(rom)
	m.MMIO.Display.Chip.ClearColLogOn = true
	m.CPU.Reset()
	m.MMIO.SweepActive = true
	m.SweepDrive = true
	m.BootToOperatingWithSweep(250_000_000)
	chip := m.MMIO.Display.Chip
	t.Logf("skipped no-area SCLRs during boot: %d", chip.SCLRNoAreaDef)
	// Summarize by (ax,ay,pattern) shape.
	shape := map[[3]int]int{}
	rwpLo, rwpHi := 1<<30, 0
	for _, e := range chip.SCLRSkipLog {
		shape[[3]int{e[1], e[2], e[3]}]++
		if e[0] < rwpLo {
			rwpLo = e[0]
		}
		if e[0] > rwpHi {
			rwpHi = e[0]
		}
	}
	for k, n := range shape {
		t.Logf("  shape ax=%d ay=%d pat=%#x : %d ops (rwp %#x..%#x)", k[0], k[1], k[2], n, rwpLo, rwpHi)
	}
}
