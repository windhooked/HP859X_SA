package device

import (
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/analog"
)

// TestSweepEngineCALPeak verifies the analog-model sweep produces a faithful
// trace: a peak at the 300 MHz CAL frequency well above the noise floor, with
// the peak landing at the correct sweep point for the default 0..2.9 GHz span.
func TestSweepEngineCALPeak(t *testing.T) {
	s := NewSweepEngine()

	// Sample every point's level; find the peak ADC count and its position.
	var peakADC uint16
	peakPt := -1
	var floorSum int
	floorN := 0
	for p := 0; p < s.Points; p++ {
		adc := s.levelToADC(s.LevelAt(p))
		if adc > peakADC {
			peakADC, peakPt = adc, p
		}
	}
	// noise floor = mean ADC away from the peak
	for p := 0; p < s.Points; p++ {
		if d := p - peakPt; d < -10 || d > 10 {
			floorSum += int(s.levelToADC(s.LevelAt(p)))
			floorN++
		}
	}
	floor := floorSum / floorN

	// CAL at 300 MHz over 0..2.9 GHz → point ≈ 300e6/2.9e9*400 ≈ 41.
	wantPt := int(300e6 / 2.9e9 * float64(s.Points-1))
	if peakPt < wantPt-5 || peakPt > wantPt+5 {
		t.Errorf("CAL peak at point %d, want ≈%d", peakPt, wantPt)
	}
	// CAL (-20 dBm) at 0 dBm ref over an 80 dB window → ~0.75 full scale.
	if peakADC < videoADCFull*2/3 {
		t.Errorf("CAL peak ADC=%#x too low (want ≳ %#x)", peakADC, videoADCFull*2/3)
	}
	// peak must stand well clear of the noise floor.
	if int(peakADC) < floor+0x80 {
		t.Errorf("peak %#x not above noise floor %#x", peakADC, floor)
	}
}

// TestSweepEngineTunes verifies the trace follows tuning: zooming the span to
// the CAL frequency moves the peak to screen centre, and an injected tone shows
// at its own point/level. This is the M3 "trace follows CF/span" behaviour.
func TestSweepEngineTunes(t *testing.T) {
	s := NewSweepEngine()
	s.StartHz, s.StopHz = 290e6, 310e6 // zoom to the 300 MHz CAL (span 20 MHz)
	peak := peakPoint(s)
	if mid := s.Points / 2; peak < mid-8 || peak > mid+8 {
		t.Errorf("zoomed CAL peak at point %d, want ≈centre %d", peak, mid)
	}

	// inject a tone at 1.2 GHz in a 0..2 GHz span; expect a peak at its point.
	s2 := NewSweepEngine()
	s2.StartHz, s2.StopHz = 0, 2e9
	s2.Spectrum.Signals = []analog.Signal{{Hz: 1.2e9, DBm: -25}}
	want := int(1.2e9 / 2e9 * float64(s2.Points-1))
	// the injected tone is the strongest above the CAL near 300 MHz only if it
	// is higher; verify a peak exists at the injected point's bucket.
	if adc := s2.levelToADC(s2.LevelAt(want)); adc < videoADCFull/2 {
		t.Errorf("injected tone at point %d ADC=%#x too low", want, adc)
	}
}

func peakPoint(s *SweepEngine) int {
	best, bp := uint16(0), -1
	for p := 0; p < s.Points; p++ {
		if a := s.levelToADC(s.LevelAt(p)); a > best {
			best, bp = a, p
		}
	}
	return bp
}

// TestSweepEngineDetectAdvances verifies DetectADC walks the sweep and wraps.
func TestSweepEngineDetectAdvances(t *testing.T) {
	s := NewSweepEngine()
	first := s.DetectADC()
	for i := 1; i < s.Points; i++ {
		s.DetectADC()
	}
	if wrapped := s.DetectADC(); wrapped != first {
		t.Errorf("after a full sweep DetectADC=%#x, want wrap to %#x", wrapped, first)
	}
}
