package device

// FPKey is a named HP 8593A front-panel key. Each key corresponds to a specific
// bit in the 6-byte key matrix the front-panel µC reports over the nibble
// registers 0xEF4001–0xEF4017. The (Byte, Bit) positions are probed with
// cmd/keymatrix; values of {255,255} are stubs yet to be measured.
//
// Key groups follow the physical layout documented in the HP 8590 User's Guide
// and visible in docs/8593E.bmp.
type FPKey struct {
	Name string
	Byte int // byte index 0–5 in the 6-byte key-matrix bitmap
	Bit  int // bit index 0–7 within that byte (0 = LSB)
}

// MatrixBit returns whether the (Byte,Bit) position has been measured.
func (k FPKey) Known() bool { return k.Byte != 255 }

// InjectMatrix presses the given key in a 6-byte matrix array.
func (k FPKey) InjectMatrix(m *[6]byte) {
	if k.Known() {
		m[k.Byte] |= 1 << uint(k.Bit)
	}
}

// FrontPanelKeys is the complete ordered key map for the HP 8593A/8590-series
// front panel. Byte/Bit positions are filled in incrementally as they are
// measured using cmd/keymatrix; stubs use {255,255}.
//
// Physical layout (left→right, top→bottom):
//   INSTRUMENT STATE:   PRESET, CONFIG, CAL, AUX CTRL, COPY
//   Row 2:              MODE, (LOCAL=CONFIG alt), SAVE, RECALL, MEAS/USER, SGL SWP
//   Left column:        FREQUENCY, SPAN, AMPLITUDE
//   WINDOWS:            ON, NEXT, ZOOM
//   MARKER:             MKR, MKR→, MKR FCTN, PEAK SEARCH
//   CONTROL:            SWEEP, BW, TRIG, AUTO COUPLE, TRACE, DISPLAY
//   DATA keypad:        7,8,9 / 4,5,6 / 1,2,3 / 0,.,BK SP + unit keys + ENTER
//   STEP:               ↑, ↓
var FrontPanelKeys = struct {
	// INSTRUMENT STATE
	Preset   FPKey
	Config   FPKey
	Cal      FPKey
	AuxCtrl  FPKey
	Copy     FPKey
	// Row 2
	Mode     FPKey
	Save     FPKey
	Recall   FPKey
	MeasUser FPKey
	SglSwp   FPKey
	// Left column
	Frequency FPKey
	Span      FPKey
	Amplitude FPKey
	// WINDOWS
	WinOn   FPKey
	WinNext FPKey
	WinZoom FPKey
	// MARKER
	Mkr        FPKey
	MkrDelta   FPKey
	MkrFctn    FPKey
	PeakSearch FPKey
	// CONTROL
	Sweep      FPKey
	BW         FPKey
	Trig       FPKey
	AutoCouple FPKey
	Trace      FPKey
	Display    FPKey
	// DATA keypad — digits
	Key0 FPKey
	Key1 FPKey
	Key2 FPKey
	Key3 FPKey
	Key4 FPKey
	Key5 FPKey
	Key6 FPKey
	Key7 FPKey
	Key8 FPKey
	Key9 FPKey
	// DATA keypad — special
	KeyDecimal FPKey
	KeyBkSp    FPKey
	KeyEnter   FPKey
	// Unit keys (share DATA pad)
	UnitGHzDBm  FPKey // GHz / +dBm / dB
	UnitMHzDBm  FPKey // MHz / −dBm / sec
	UnitKHzmV   FPKey // kHz / mV / ms
	UnitHzuV    FPKey // Hz / µV / µs
	// STEP
	StepUp   FPKey
	StepDown FPKey
	// Softkeys 1–6 (right-side, context-dependent)
	SK1 FPKey
	SK2 FPKey
	SK3 FPKey
	SK4 FPKey
	SK5 FPKey
	SK6 FPKey
}{
	// INSTRUMENT STATE — byte/bit positions measured with cmd/keymatrix
	// (stubs {255,255} to be filled in; fill with measured values as found)
	Preset:   FPKey{"PRESET", 255, 255},
	Config:   FPKey{"CONFIG", 255, 255},
	Cal:      FPKey{"CAL", 255, 255},
	AuxCtrl:  FPKey{"AUX CTRL", 255, 255},
	Copy:     FPKey{"COPY", 255, 255},
	Mode:     FPKey{"MODE", 255, 255},
	Save:     FPKey{"SAVE", 255, 255},
	Recall:   FPKey{"RECALL", 255, 255},
	MeasUser: FPKey{"MEAS/USER", 255, 255},
	SglSwp:   FPKey{"SGL SWP", 255, 255},
	// Left column
	Frequency: FPKey{"FREQUENCY", 255, 255},
	Span:      FPKey{"SPAN", 255, 255},
	Amplitude: FPKey{"AMPLITUDE", 255, 255},
	// WINDOWS
	WinOn:   FPKey{"WIN ON", 255, 255},
	WinNext: FPKey{"WIN NEXT", 255, 255},
	WinZoom: FPKey{"WIN ZOOM", 255, 255},
	// MARKER
	Mkr:        FPKey{"MKR", 255, 255},
	MkrDelta:   FPKey{"MKR→", 255, 255},
	MkrFctn:    FPKey{"MKR FCTN", 255, 255},
	PeakSearch: FPKey{"PEAK SEARCH", 255, 255},
	// CONTROL
	Sweep:      FPKey{"SWEEP", 255, 255},
	BW:         FPKey{"BW", 255, 255},
	Trig:       FPKey{"TRIG", 255, 255},
	AutoCouple: FPKey{"AUTO COUPLE", 255, 255},
	Trace:      FPKey{"TRACE", 255, 255},
	Display:    FPKey{"DISPLAY", 255, 255},
	// DATA keypad digits
	Key0: FPKey{"0", 255, 255},
	Key1: FPKey{"1", 255, 255},
	Key2: FPKey{"2", 255, 255},
	Key3: FPKey{"3", 255, 255},
	Key4: FPKey{"4", 255, 255},
	Key5: FPKey{"5", 255, 255},
	Key6: FPKey{"6", 255, 255},
	Key7: FPKey{"7", 255, 255},
	Key8: FPKey{"8", 255, 255},
	Key9: FPKey{"9", 255, 255},
	KeyDecimal: FPKey{".", 255, 255},
	KeyBkSp:    FPKey{"BK SP", 255, 255},
	KeyEnter:   FPKey{"ENTER", 255, 255},
	// Unit keys
	UnitGHzDBm: FPKey{"GHz/+dBm/dB", 255, 255},
	UnitMHzDBm: FPKey{"MHz/−dBm/sec", 255, 255},
	UnitKHzmV:  FPKey{"kHz/mV/ms", 255, 255},
	UnitHzuV:   FPKey{"Hz/µV/µs", 255, 255},
	// STEP
	StepUp:   FPKey{"STEP↑", 255, 255},
	StepDown: FPKey{"STEP↓", 255, 255},
	// Softkeys
	SK1: FPKey{"SK1", 255, 255},
	SK2: FPKey{"SK2", 255, 255},
	SK3: FPKey{"SK3", 255, 255},
	SK4: FPKey{"SK4", 255, 255},
	SK5: FPKey{"SK5", 255, 255},
	SK6: FPKey{"SK6", 255, 255},
}
