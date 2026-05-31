package analog

import (
	"math"
	"testing"
)

func approx(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

// TestFrequencyModel_Tuning: coarse DAC spans the band; tuned f = YTO - 1st IF.
func TestFrequencyModel_Tuning(t *testing.T) {
	var f FrequencyModel
	// neutralise fine/FM (midscale contributes 0 offset).
	f.FineDAC, f.FMDAC = dacMax/2, dacMax/2
	// Coarse 0 → YTO 3.0 GHz → tuned = 3.0 - 3.9214 = -0.9214 GHz (below band;
	// the model is linear, band edges are the firmware's concern).
	f.CoarseDAC = 0
	if got := f.ytoHz(); !approx(got, 3.0e9, 1e6) {
		t.Fatalf("coarse=0 YTO=%g, want 3.0 GHz", got)
	}
	// Coarse mid → YTO mid-band; tuned should be ~ (mid - IF).
	f.CoarseDAC = dacMax
	if got := f.ytoHz(); !approx(got, 6.8214e9, 1e6) {
		t.Fatalf("coarse=max YTO=%g, want 6.8214 GHz", got)
	}
	// Monotonic: higher coarse → higher tuned (servo convergence relies on this).
	f.CoarseDAC = 1000
	lo := f.TunedHz()
	f.CoarseDAC = 2000
	if f.TunedHz() <= lo {
		t.Fatal("tuned freq not monotonic in coarse DAC")
	}
}

// TestCounterlock_Servo: the count is a faithful, monotonic function of the YTO
// DAC, so the firmware's tune servo converges.
func TestCounterlock_Servo(t *testing.T) {
	var f FrequencyModel
	const div = 100e3
	f.CoarseDAC = 1000
	c1 := f.CounterlockCount(div)
	f.CoarseDAC = 1001
	c2 := f.CounterlockCount(div)
	if c2 <= c1 {
		t.Fatalf("counterlock count not monotonic: %d then %d", c1, c2)
	}
	// reproducible (no randomness) — same DAC → same count.
	f.CoarseDAC = 1000
	if f.CounterlockCount(div) != c1 {
		t.Fatal("counterlock count not reproducible")
	}
}

// TestSpectrum_NoiseAndCal: noise floor is low; the 300 MHz CAL produces a clear
// peak when on.
func TestSpectrum_NoiseAndCal(t *testing.T) {
	s := SpectrumModel{CalSignalOn: true, RBWHz: 1e6}
	off := s.LevelDBm(100e6) // away from the CAL tone → noise floor
	on := s.LevelDBm(300e6)  // at the CAL tone
	if !(off < -70) {
		t.Fatalf("noise floor too high: %g dBm", off)
	}
	if !(on > off+40) {
		t.Fatalf("CAL peak (%g) not above noise (%g)", on, off)
	}
	if !approx(on, calSignalDBm, 1.0) {
		t.Fatalf("CAL peak = %g dBm, want ~%g", on, calSignalDBm)
	}
	// CAL off → no peak.
	s.CalSignalOn = false
	if p := s.LevelDBm(300e6); p > -70 {
		t.Fatalf("CAL peak present with cal off: %g", p)
	}
}

// TestDetector_MU: MU scaling matches dBm = (MU-8000)*0.01 + refLevel.
func TestDetector_MU(t *testing.T) {
	d := Detector{RefLevelDBm: 0} // ref level 0 dBm
	if got := d.MU(0); got != 8000 {
		t.Fatalf("0 dBm at ref 0 → MU %d, want 8000 (top)", got)
	}
	if got := d.MU(-20); got != 6000 { // -20 dB = 2 div below top
		t.Fatalf("-20 dBm → MU %d, want 6000", got)
	}
	if got := d.MU(-90); got != 0 { // below bottom → clamp
		t.Fatalf("-90 dBm → MU %d, want 0 (clamped bottom)", got)
	}
}
