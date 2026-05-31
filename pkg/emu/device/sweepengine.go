package device

import "github.com/windhooked/HP859X_SA/pkg/emu/analog"

// SweepEngine produces the video-ADC reading the firmware samples at 0xFFF200
// for each point of a trace sweep, using the semi-physical analog model
// (pkg/emu/analog): a frequency-domain input spectrum (thermal noise floor + the
// internal 300 MHz CAL signal + injected tones) shaped by the detector into the
// raw video-ADC count.
//
// This replaces the earlier hand-tuned `sweepDetector` placeholder (a bare
// Gaussian bump) so the trace data is faithful: a real CAL peak at 300 MHz and a
// noise floor at the modelled level. The firmware reads 0xFFF200 once per
// ADC_SYNC during a sweep (IRQ6 capture handler), storing each count into the
// trace buffer; SweepEngine.DetectADC supplies that count and advances the
// sweep position.
//
// Frequency mapping: point p of Points maps linearly across [StartHz, StopHz]
// (band-0 default 0..2.9 GHz). Video-ADC mapping: the display covers an 80 dB
// window (8 divisions × 10 dB/div) from RefLevelDBm (top, full scale) down to
// RefLevelDBm-80 (bottom, zero); levels are clamped to that window.
type SweepEngine struct {
	Spectrum analog.SpectrumModel
	Detector analog.Detector
	StartHz  float64 // sweep start frequency
	StopHz   float64 // sweep stop frequency
	Points   int     // samples per sweep (401 on the 8593)
	pos      int     // current sweep position (advances per DetectADC)
}

// videoADCFull is the full-scale 0xFFF200 video-ADC reading (top of screen).
// The detector ADC is ~9-bit; the firmware scales the count into measurement
// units itself, so only the relative shape (peak vs floor) matters here.
const videoADCFull = 0x1FF

// NewSweepEngine returns a SweepEngine wired to the analog model with band-0
// defaults: 0..2.9 GHz span, 0 dBm reference level, the 300 MHz CAL signal on,
// 1 MHz RBW, 401 points — so a freshly-swept trace shows the CAL peak.
func NewSweepEngine() *SweepEngine {
	return &SweepEngine{
		Spectrum: analog.SpectrumModel{CalSignalOn: true, RBWHz: 1e6},
		Detector: analog.Detector{RefLevelDBm: 0},
		StartHz:  0,
		StopHz:   2.9e9,
		Points:   401,
	}
}

// freqAt returns the centre input frequency tuned at sweep point p.
func (s *SweepEngine) freqAt(p int) float64 {
	if s.Points <= 1 {
		return s.StartHz
	}
	return s.StartHz + float64(p)/float64(s.Points-1)*(s.StopHz-s.StartHz)
}

// bucketPeakDBm peak-detects the input spectrum across point p's frequency
// bucket (one point's worth of span). On a real analyzer the per-point detector
// captures the peak within the bucket as the sweep passes through, so a CW tone
// narrower than the point spacing still shows at its true level in the bucket
// that contains it (rather than being missed between point samples).
func (s *SweepEngine) bucketPeakDBm(p int) float64 {
	pts := s.Points
	if pts <= 1 {
		return s.Spectrum.LevelDBm(s.StartHz)
	}
	bw := (s.StopHz - s.StartHz) / float64(pts-1) // bucket width
	center := s.freqAt(p)
	best := s.Spectrum.LevelDBm(center)
	const sub = 32
	for i := 0; i <= sub; i++ {
		f := center - bw/2 + bw*float64(i)/sub
		if l := s.Spectrum.LevelDBm(f); l > best {
			best = l
		}
	}
	return best
}

// levelToADC maps an input level (dBm) to the 0..videoADCFull video-ADC count
// for the current reference level, clamped to the 80 dB display window.
func (s *SweepEngine) levelToADC(dBm float64) uint16 {
	frac := (dBm - (s.Detector.RefLevelDBm - 80)) / 80
	v := int(frac * videoADCFull)
	if v < 0 {
		v = 0
	}
	if v > videoADCFull {
		v = videoADCFull
	}
	return uint16(v)
}

// DetectADC returns the video-ADC reading for the current sweep position and
// advances. The position wraps at Points so a continuously-driven sweep repeats.
func (s *SweepEngine) DetectADC() uint16 {
	pts := s.Points
	if pts <= 0 {
		pts = 401
	}
	p := s.pos % pts
	s.pos++
	return s.levelToADC(s.bucketPeakDBm(p))
}

// Reset rewinds the sweep position (retrace).
func (s *SweepEngine) Reset() { s.pos = 0 }

// LevelAt returns the modelled input level (dBm) at sweep point p — for tests
// and trace-buffer rendering without mutating sweep position.
func (s *SweepEngine) LevelAt(p int) float64 { return s.bucketPeakDBm(p) }
