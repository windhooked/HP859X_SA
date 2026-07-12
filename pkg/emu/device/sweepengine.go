package device

import (
	"math"
	"math/rand"

	"github.com/windhooked/HP859X_SA/pkg/emu/analog"
)

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

	// NoiseFloorDBm is the centre level of the displayed "grass" noise floor
	// added per point. It is raised onto the screen (vs the analog model's true
	// ~-90 dBm thermal floor, which is off-screen at a 0 dBm reference) so the
	// trace shows the classic noisy SA baseline. NoiseAmpDB is the peak random
	// variation added on top (per point), giving the grass its texture.
	NoiseFloorDBm float64
	NoiseAmpDB    float64

	// Tune, when TuneActive, derives the sweep CENTRE frequency from the
	// firmware's real YTO coil DACs (via analog.FrequencyModel) instead of the
	// fixed StartHz/StopHz — so the displayed spectrum tracks wherever the
	// firmware has actually tuned the YTO (★ 2026-07-12 A7 map; the machine
	// feeds Tune's DACs from HP8593AMMIO.YTOCoilDACs each sweep). The window is
	// [centre-SpanHz/2, centre+SpanHz/2], clamped to ≥0.
	//
	// SpanHz is the swept width. It stays a CONFIGURED value (band-0 default
	// 2.9 GHz) — the span→Hz DAC mapping is not yet reverse-engineered (a single
	// coil-DAC snapshot gives the centre; the analog ramp generates the span),
	// so only the CENTRE is DAC-derived today. See docs/A7_ANALOG_IO_BUS.md
	// "Still open" and freqAt.
	Tune       analog.FrequencyModel
	TuneActive bool
	SpanHz     float64

	pos int        // current sweep position (advances per DetectADC)
	rng *rand.Rand // per-point noise generator (seeded → reproducible)
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
		SpanHz:   2.9e9, // band-0 default swept width (see SpanHz doc)
		// Visible grass: centre ~10% up the 80 dB window (−72 dBm at 0 dBm ref)
		// with ±6 dB of per-point random texture. Seeded for reproducibility.
		NoiseFloorDBm: -72,
		NoiseAmpDB:    6,
		rng:           rand.New(rand.NewSource(0x8593)),
	}
}

// Window returns the effective [start, stop] sweep frequencies (the DAC-derived
// window when TuneActive, else the fixed StartHz/StopHz) — for diagnostics and
// the GUI frequency-axis labels.
func (s *SweepEngine) Window() (start, stop float64) { return s.window() }

// window returns the effective [start, stop] sweep frequencies. When TuneActive
// (and the firmware has tuned the YTO — non-zero coil DACs), it derives the
// centre from the live DACs and applies SpanHz around it, clamped to ≥0; else
// it falls back to the fixed StartHz/StopHz. This is the one place the firmware
// tuning enters the frequency axis.
func (s *SweepEngine) window() (start, stop float64) {
	if s.TuneActive && (s.Tune.CoarseDAC != 0 || s.Tune.FineDAC != 0 || s.Tune.FMDAC != 0) {
		center := s.Tune.TunedHz()
		span := s.SpanHz
		if span <= 0 {
			span = s.StopHz - s.StartHz
		}
		start, stop = center-span/2, center+span/2
		if start < 0 {
			start = 0
		}
		return start, stop
	}
	return s.StartHz, s.StopHz
}

// SetSignals validates and installs the injected CW tones (the signal
// boundary/limit check). A signal whose frequency is outside the sweep span
// [StartHz, StopHz] is DROPPED — it cannot appear in any point bucket — and its
// amplitude is clamped to the display window [RefLevelDBm-80, RefLevelDBm].
// Returns how many signals were dropped for being out of band.
func (s *SweepEngine) SetSignals(sigs []analog.Signal) (dropped int) {
	lo, hi := s.Detector.RefLevelDBm-80, s.Detector.RefLevelDBm
	start, stop := s.window()
	valid := make([]analog.Signal, 0, len(sigs))
	for _, sig := range sigs {
		if sig.Hz < start || sig.Hz > stop {
			dropped++
			continue
		}
		if sig.DBm > hi {
			sig.DBm = hi
		}
		if sig.DBm < lo {
			sig.DBm = lo
		}
		valid = append(valid, sig)
	}
	s.Spectrum.Signals = valid
	return dropped
}

// freqAt returns the centre input frequency tuned at sweep point p.
func (s *SweepEngine) freqAt(p int) float64 {
	start, stop := s.window()
	if s.Points <= 1 {
		return start
	}
	return start + float64(p)/float64(s.Points-1)*(stop-start)
}

// bucketPeakDBm peak-detects the input spectrum across point p's frequency
// bucket (one point's worth of span). On a real analyzer the per-point detector
// captures the peak within the bucket as the sweep passes through, so a CW tone
// narrower than the point spacing still shows at its true level in the bucket
// that contains it (rather than being missed between point samples).
func (s *SweepEngine) bucketPeakDBm(p int) float64 {
	pts := s.Points
	start, stop := s.window()
	if pts <= 1 {
		return s.Spectrum.LevelDBm(start)
	}
	bw := (stop - start) / float64(pts-1) // bucket width
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
	sig := s.bucketPeakDBm(p) // spectrum: signals + the model's true thermal floor
	// Add the displayed grass: a random level around NoiseFloorDBm, power-summed
	// with the spectrum so a real tone rises cleanly above the noise.
	if s.rng != nil && s.NoiseAmpDB != 0 {
		grass := s.NoiseFloorDBm + s.rng.Float64()*s.NoiseAmpDB
		return s.levelToADC(powerSumDBm(sig, grass))
	}
	return s.levelToADC(sig)
}

// powerSumDBm returns 10·log10(10^(a/10) + 10^(b/10)) — the dBm level of two
// incoherent contributions summed in linear power.
func powerSumDBm(a, b float64) float64 {
	return 10 * math.Log10(math.Pow(10, a/10)+math.Pow(10, b/10))
}

// Reset rewinds the sweep position (retrace).
func (s *SweepEngine) Reset() { s.pos = 0 }

// LevelAt returns the modelled input level (dBm) at sweep point p — for tests
// and trace-buffer rendering without mutating sweep position.
func (s *SweepEngine) LevelAt(p int) float64 { return s.bucketPeakDBm(p) }
