package machine_test

import (
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/analog"
)

// TestA7TuneDiag boots the firmware with the sweep model and dumps the A7 tune
// state the firmware actually programmed: the direct YTO coil-DAC ports
// (F700/F702/F704 = FM/fine/coarse), the reg-0 serial chain, the timebase DAC,
// and the frequency the FrequencyModel derives from those coil DACs. This is a
// MEASUREMENT (not an assertion) to ground the FrequencyModel↔SweepEngine
// wiring — run with -run TestA7TuneDiag -v. See docs/A7_ANALOG_IO_BUS.md.
func TestA7TuneDiag(t *testing.T) {
	if testing.Short() {
		t.Skip("boot-length test")
	}
	m := newMachine(t)
	m.MMIO.SweepActive = true
	m.BootToOperatingWithSweep(200_000_000)

	fm, fine, coarse := m.MMIO.YTOCoilDACs()
	g0, g1, g2 := m.MMIO.A7YTOChain()
	t.Logf("YTO coil DACs: FM(F700)=%#04x  fine(F702)=%#04x  coarse(F704)=%#04x", fm, fine, coarse)
	t.Logf("reg-0 serial chain: g0(8b)=%#02x g1(12b)=%#04x g2(12b)=%#04x", g0, g1, g2)
	t.Logf("timebase DAC (reg5)=%#02x  settle strobes=%d", m.MMIO.A7TimebaseDAC(), m.MMIO.A7SettleStrobes())

	freq := analog.FrequencyModel{
		CoarseDAC: int(coarse),
		FineDAC:   int(fine),
		FMDAC:     int(fm),
	}
	tuned := freq.TunedHz()
	t.Logf("derived: YTO=%.4f GHz  tuned(YTO-IF)=%.4f GHz", freq.YTOHz()/1e9, tuned/1e9)

	// Assertions (measured 2026-07-12: coarse≈0x08e8 → tuned≈1.21 GHz):
	// the firmware DID tune the YTO (non-zero coarse DAC), and the derived
	// centre is a plausible band-0 input frequency (0..2.9 GHz). This is the
	// evidence that the SweepEngine's DAC-derived window (syncSweepTune) rides
	// on a real, firmware-programmed value — not a fabricated span.
	if coarse == 0 {
		t.Fatal("YTO coarse DAC (F704) is 0 — firmware did not tune the YTO")
	}
	if tuned < 0 || tuned > 2.9e9 {
		t.Errorf("derived tuned centre = %.3f GHz, outside band-0 (0..2.9 GHz)", tuned/1e9)
	}
	// The SweepEngine must have picked up the same tuning (TuneActive set by
	// syncSweepTune during the sweep-driven boot).
	if !m.MMIO.Sweep.TuneActive {
		t.Error("SweepEngine.TuneActive not set — syncSweepTune never ran during boot")
	}
	if got := m.MMIO.Sweep.Tune.CoarseDAC; got != int(coarse) {
		t.Errorf("SweepEngine coarse DAC = %#x, want %#x (live port)", got, coarse)
	}
}
