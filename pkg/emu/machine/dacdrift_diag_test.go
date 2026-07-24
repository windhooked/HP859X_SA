package machine

import (
	"os"
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

// Scratch: coil-DAC dynamics during the natural sweep run — sawtooth ramp or
// quasi-static walk?
func TestDACDriftDiag(t *testing.T) {
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
	var lastFM, lastFine, lastCoarse uint16 = 0xFFFF, 0xFFFF, 0xFFFF
	changes := 0
	for i := 0; i < 300; i++ { // 60M cycles, log every 200k
		m.bootLoop(200_000, nil)
		fm, fine, coarse := m.MMIO.YTOCoilDACs()
		if fm != lastFM || fine != lastFine || coarse != lastCoarse {
			if changes < 60 {
				t.Logf("cyc %6.1fM: FM=%#05x fine=%#05x coarse=%#05x", float64(i)*0.2, fm, fine, coarse)
			}
			changes++
			lastFM, lastFine, lastCoarse = fm, fine, coarse
		}
	}
	t.Logf("total DAC-change samples: %d / 300", changes)
}
