package machine

import (
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

// TestCoreTracksFirmwareGeometry verifies Phase 2 of the ACRTC rebuild
// (docs/HD63484_REBUILD_PROMPT.md): the faithful flat-address core, dual-written
// by the live decoder, ends a boot holding the firmware's REAL display geometry —
// ORG decoded to layer 1 / dpa=0x3a45, MWR1=64 — and has accumulated pen content
// into the unified address space (non-empty buffer). This pins that the core is
// correctly fed before Phase 4 switches rendering onto it.
func TestCoreTracksFirmwareGeometry(t *testing.T) {
	rom, err := romloader.LoadDir("../../../hp8593a_eeproms")
	if err != nil {
		t.Skip("rom not available")
	}
	m, _ := New8593A(rom)
	m.CPU.Reset()
	m.MMIO.SweepActive = true
	m.SweepDrive = true
	m.BootToOperatingWithSweep(250_000_000)

	c := m.MMIO.Display.Chip
	gotDN, gotDPA, gotMWR1 := c.CoreORG()
	if gotDN != 1 {
		t.Errorf("core ORG layer = %d, want 1 (firmware draws on screen 1)", gotDN)
	}
	if gotDPA != 0x3a45 {
		t.Errorf("core ORG dpa = 0x%05x, want 0x3a45 (firmware ORG 0x4003,0xa450)", gotDPA)
	}
	if gotMWR1 != 64 {
		t.Errorf("core MWR1 = %d, want 64", gotMWR1)
	}

	// The core must have accumulated drawn content via dual-write (not be blank).
	if lit := c.CoreLitWords(); lit < 100 {
		t.Errorf("core buffer has only %d lit words after boot — dual-write not feeding it", lit)
	}
}
