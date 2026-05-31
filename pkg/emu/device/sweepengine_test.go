package device

import "testing"

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
