package machine

import (
	"os"
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

// TestCalExtentDiag measures the exact cal-NVRAM (0x200000) byte extent the
// firmware actually touches during a faithful boot — the authoritative "what to
// back up" window for the GPIB cal dump. DIAG=1.
func TestCalExtentDiag(t *testing.T) {
	if os.Getenv("DIAG") == "" {
		t.Skip("DIAG=1")
	}
	rom, err := romloader.LoadDir("../../../hp8593a_eeproms")
	if err != nil {
		t.Skip("rom")
	}
	m, _ := New8593A(rom)
	minR, maxR := uint32(0xFFFFFFFF), uint32(0)
	minW, maxW := uint32(0xFFFFFFFF), uint32(0)
	pages := map[uint32]int{}
	nR, nW := 0, 0
	m.CalNVRAM.Trace = func(off uint32, sz bus.Size, val uint32, write bool) {
		end := off + uint32(sz) - 1
		pages[off>>10]++
		if write {
			nW++
			if off < minW {
				minW = off
			}
			if end > maxW {
				maxW = end
			}
		} else {
			nR++
			if off < minR {
				minR = off
			}
			if end > maxR {
				maxR = end
			}
		}
	}
	m.CPU.Reset()
	m.MMIO.SweepActive = true
	m.SweepDrive = true
	// Faithful boot (no LoopBreaker cal shortcut) so the real checksum sweep runs.
	m.BootToOperatingWithSweep(150_000_000)

	t.Logf("cal-NVRAM (CPU 0x200000+): reads=%d [off %#x..%#x], writes=%d [off %#x..%#x]",
		nR, minR, maxR, nW, minW, maxW)
	t.Logf("distinct 1KB pages touched: %d", len(pages))
	// Show contiguous touched ranges (page granularity).
	var lo, hi = -1, -1
	flush := func() {
		if lo >= 0 {
			t.Logf("  touched pages %#x..%#x  (CPU 0x2%05X..0x2%05X)", lo, hi, lo<<10, (hi<<10)|0x3FF)
		}
	}
	for p := 0; p < 64; p++ {
		if pages[uint32(p)] > 0 {
			if lo < 0 {
				lo = p
			}
			hi = p
		} else if lo >= 0 {
			flush()
			lo, hi = -1, -1
		}
	}
	flush()
}
