// Package analog is the semi-physical analog-subsystem model for the virtual
// HP 8593A: it turns the firmware's DAC settings into a tuned frequency, a
// frequency-domain input spectrum (thermal noise floor + the internal 300 MHz
// CAL signal + any injected signals), a detected video level, and the
// readbacks the firmware polls (the A25 counterlock count, lock/settle status).
//
// It models input→output BEHAVIOUR as the firmware observes it through the
// control/readback registers — NOT the actual RF circuits. Built from the
// 08590-90316 service guide, the 08590-90235 programmer's guide, the CLIP, and
// the project firmware RE (see docs/ANALOG_MODEL_PLAN.md).
//
// Units & scales (ground truth):
//   - Trace measurement units (MU): 0..8000, 8000 = reference level (top),
//     1000 MU/div = 10 dB/div, 1 MU = 0.01 dB.
//     dBm = (MU-8000)*0.01 + refLevelDBm.
//   - First LO (YTO), band 0 (8593E): 3.0..6.8214 GHz; 1st IF 3.9214 GHz, so
//     tuned input f = YTO - 3.9214 GHz (band 0, 9 kHz..2.9 GHz).
//   - A7 tuning DACs: 12-bit (0..4095). REF_LVL_CAL: 8-bit (0..255), default 200.
package analog

import "math"

// Constants from the service guide (8593E, band 0).
const (
	firstIFHz     = 3.9214e9 // band-0 1st IF
	ytoLoHz       = 3.0e9    // YTO low end (coarse DAC = 0)
	ytoHiHz       = 6.8214e9 // YTO high end (coarse DAC = 4095)
	calSignalHz   = 300e6    // internal CAL OUT
	calSignalDBm  = -20.0    // CAL OUT level
	noiseFloorDBm = -90.0    // displayed thermal noise floor (band 0, ~RBW-dependent)

	dacMax = 4095 // 12-bit YTO/span DAC full scale
)

// FrequencyModel maps the A7 YTO tune DACs to a first-LO / tuned frequency and
// provides the A25 counterlock count the firmware servos against. Because the
// firmware iterates the YTO DAC until the counted frequency == target, the
// count MUST be a faithful function of the programmed DAC, or CAL FREQ / the
// YTO-tune loop never converges (→ FREQ UNCAL).
type FrequencyModel struct {
	CoarseDAC int // YTO coarse tune (0..4095)
	FineDAC   int // YTO fine tune (0..4095)
	FMDAC     int // YTO FM/extra-fine tune (0..4095)
	SpanDAC   int // sweep span (0..4095)
}

// ytoHz returns the modelled YTO (first LO) frequency for the current tune DACs.
// Coarse spans the full YTO range; fine/FM add small offsets so the firmware's
// successive-approximation tune (coarse, then fine, then FM) can null the error.
func (f *FrequencyModel) ytoHz() float64 {
	coarse := clampDAC(f.CoarseDAC)
	span := (ytoHiHz - ytoLoHz)
	base := ytoLoHz + float64(coarse)/dacMax*span
	// fine: ±~1 MHz over the DAC range; FM: ±~30 kHz. Signs per the YTO
	// convention (more negative tune voltage → higher frequency); the firmware
	// only needs a monotonic, consistent response to converge.
	base += (float64(clampDAC(f.FineDAC)) - dacMax/2) / dacMax * 2e6
	base += (float64(clampDAC(f.FMDAC)) - dacMax/2) / dacMax * 60e3
	return base
}

// TunedHz is the input frequency currently tuned (band 0): YTO - 1st IF.
func (f *FrequencyModel) TunedHz() float64 { return f.ytoHz() - firstIFHz }

// CounterlockCount returns the A25 divided-sampler-IF count the firmware reads
// back to verify the YTO is on target. The firmware computes
// firstLO = N*F_SO + samplerIF and counts samplerIF/10; we return a value
// proportional to the YTO frequency so the servo converges when the DAC is set
// to the target. divHz is the counter's Hz-per-count resolution.
func (f *FrequencyModel) CounterlockCount(divHz float64) int {
	if divHz <= 0 {
		divHz = 1
	}
	return int(math.Round(f.ytoHz() / divHz))
}

// SpectrumModel is the frequency-domain "input": the thermal noise floor plus
// the internal 300 MHz CAL signal plus any injected signals. LevelDBm(f)
// returns the input power (dBm) the analyzer would see at frequency f.
type SpectrumModel struct {
	// Injected test signals (Hz → dBm), in addition to noise + CAL.
	Signals []Signal
	// CalSignalOn routes the internal 300 MHz CAL OUT to the input (as during
	// CAL AMPTD / a boxed analyzer with the cal cable connected).
	CalSignalOn bool
	// RBWHz is the resolution bandwidth — sets the noise floor (narrower → lower)
	// and the displayed peak width.
	RBWHz float64
}

// Signal is an injected CW tone.
type Signal struct {
	Hz  float64
	DBm float64
}

// LevelDBm returns the input level at frequency f: a power-sum of the noise
// floor and every signal shaped by the RBW response (a simple Gaussian-ish
// resolution filter centred on each tone).
func (s *SpectrumModel) LevelDBm(f float64) float64 {
	rbw := s.RBWHz
	if rbw <= 0 {
		rbw = 1e6
	}
	// noise floor lowers ~10 dB per decade of RBW reduction from 1 MHz.
	noise := noiseFloorDBm + 10*math.Log10(rbw/1e6)
	powmW := dbmToMW(noise)
	add := func(sigHz, sigDBm float64) {
		// resolution-filter shape: -3 dB at ±RBW/2, rolls off beyond.
		df := (f - sigHz) / (rbw / 2)
		atten := -3.0 * df * df // dB; ≈Gaussian skirt near the peak
		if atten < -120 {
			return
		}
		powmW += dbmToMW(sigDBm + atten)
	}
	if s.CalSignalOn {
		add(calSignalHz, calSignalDBm)
	}
	for _, sig := range s.Signals {
		add(sig.Hz, sig.DBm)
	}
	return mwToDBm(powmW)
}

// Detector converts an input level (dBm) to a trace measurement-unit value
// (0..8000) for the current reference level, then to the raw video-ADC count
// the firmware reads. 8000 MU = reference level (top); 0 MU = bottom of screen
// (refLevel - 80 dB on a 10 dB/div log grid).
type Detector struct {
	RefLevelDBm float64 // top-of-screen reference level
}

// MU returns the measurement-unit value (clamped 0..8000) for an input level.
func (d *Detector) MU(levelDBm float64) int {
	mu := (levelDBm-d.RefLevelDBm)*100 + 8000 // 1 MU = 0.01 dB; 8000 = ref level
	if mu < 0 {
		mu = 0
	}
	if mu > 8000 {
		mu = 8000
	}
	return int(math.Round(mu))
}

// dbmToMW / mwToDBm power conversions.
func dbmToMW(dbm float64) float64 { return math.Pow(10, dbm/10) }
func mwToDBm(mw float64) float64 {
	if mw <= 0 {
		return -200
	}
	return 10 * math.Log10(mw)
}

func clampDAC(v int) int {
	if v < 0 {
		return 0
	}
	if v > dacMax {
		return dacMax
	}
	return v
}
