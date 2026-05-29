package device

import "testing"

// TestAnalogBus_StatusCadence verifies the sel=0x9A status register, when
// idle (no conversion pending), returns 0x06 every Nth read and 0 otherwise —
// the ready-pulse cadence the firmware's polls rely on for background work.
func TestAnalogBus_StatusCadence(t *testing.T) {
	var a analogBus
	a.writeSelect(abSelStatus)
	matches := 0
	for i := 0; i < statusReadyEveryNReads*4; i++ {
		v := a.readData()
		if v == adcStatusIdle { // 0x06
			matches++
		} else if v != 0 {
			t.Errorf("read #%d = %#04X, want 0x06 or 0", i, v)
		}
	}
	if matches != 4 {
		t.Errorf("matches=%d in %d reads, want 4", matches, statusReadyEveryNReads*4)
	}
}

// TestAnalogBus_StatusBitsSatisfyFirmwarePolls verifies the derived status
// bit map satisfies every distinct sel=0x9A poll contract the firmware uses
// (see docs/ANALOG_BUS_MODEL.md §2): the idle value 0x06 satisfies the
// operating-loop and init polls, and the data-ready value 0x07 satisfies the
// conversion-done polls — which 0x06 alone could not (the conflict that makes
// a state machine necessary).
func TestAnalogBus_StatusBitsSatisfyFirmwarePolls(t *testing.T) {
	const idle, ready = adcStatusIdle, adcStatusIdle | adcStatusEOC // 0x06, 0x07
	// Operating-loop poll: (0x12 & x) == 0x02 — satisfied by idle and ready.
	if 0x12&idle != 0x02 || 0x12&ready != 0x02 {
		t.Errorf("op-loop poll not satisfied: idle=%#x ready=%#x", 0x12&idle, 0x12&ready)
	}
	// Init poll: x == 0x06 (exact) — satisfied only by idle (EOC clear).
	if idle != 0x06 || ready == 0x06 {
		t.Errorf("init poll: idle=%#x (want 0x06), ready=%#x (must differ)", idle, ready)
	}
	// Conversion-done polls: (0x01 & x) == 0x01 and (0x11 & x) == 0x01 —
	// satisfied only by ready (bit 0 set), NOT by idle.
	if 0x01&ready != 0x01 || 0x11&ready != 0x01 {
		t.Errorf("conv-done poll not satisfied by ready=%#x", ready)
	}
	if 0x01&idle == 0x01 {
		t.Errorf("idle=%#x must NOT satisfy the bit-0 conv-done poll", idle)
	}
}

// TestAnalogBus_ConversionLifecycle verifies the ADC conversion state machine
// (docs/ANALOG_BUS_MODEL.md §5): idle reads 0x06; a DAC-low write triggers a
// conversion; after convReadsToEOC status reads a ready pulse reports 0x07
// (EOC); reading the 0x9F result consumes it back to 0x06.
func TestAnalogBus_ConversionLifecycle(t *testing.T) {
	var a analogBus
	pulse := func() uint16 { // advance to the next ready pulse and return it
		a.writeSelect(abSelStatus)
		var v uint16
		for i := 0; i < statusReadyEveryNReads; i++ {
			v = a.readData()
		}
		return v
	}
	// Idle: a ready pulse is 0x06 (no data ready).
	if v := pulse(); v != adcStatusIdle {
		t.Fatalf("idle pulse = %#x, want 0x06", v)
	}
	// Trigger a conversion via the DAC-low write (completes send_dac_word).
	a.writeSelect(abSelDACLo)
	a.writeData(0x10)
	// The very next pulse should report EOC (0x07) since a full pulse period
	// (>= convReadsToEOC) of status reads elapses inside pulse().
	if v := pulse(); v != adcStatusIdle|adcStatusEOC {
		t.Fatalf("post-trigger pulse = %#x, want 0x07 (EOC)", v)
	}
	// Reading the result clears EOC → idle again.
	a.writeSelect(abSelADC)
	a.readData()
	if v := pulse(); v != adcStatusIdle {
		t.Fatalf("post-read pulse = %#x, want 0x06 (EOC cleared)", v)
	}
}

// TestAnalogBus_DACComposition verifies that three byte writes via
// selects 0x95/0x96/0x97 build a 24-bit DAC value (matches the
// firmware's fcn.5E384 send_dac_word convention).
func TestAnalogBus_DACComposition(t *testing.T) {
	var a analogBus
	a.writeSelect(abSelDACHi)
	a.writeData(0x12)
	a.writeSelect(abSelDACMid)
	a.writeData(0x34)
	a.writeSelect(abSelDACLo)
	a.writeData(0x56)
	if a.dac != 0x123456 {
		t.Errorf("dac = %#06X, want 0x123456", a.dac)
	}
}

// TestAnalogBus_RegisterFile verifies that a select-then-write-then-read
// round-trip preserves the value for selects that don't have explicit
// per-select semantics — the "consistent state" property the firmware's
// control-register writes depend on.
func TestAnalogBus_RegisterFile(t *testing.T) {
	var a analogBus
	cases := []struct {
		sel uint16
		val uint16
	}{
		{abSelCtrlA, 0x0000},
		{abSelCtrlB, 0x0012},
		{abSelCtrlC, 0x000F},
		{0x42, 0xBEEF}, // unmodelled select, register-file path
	}
	for _, c := range cases {
		a.writeSelect(c.sel)
		a.writeData(c.val)
		got := a.readData()
		if got != c.val {
			t.Errorf("sel=0x%02X: write %#04X, read %#04X — round-trip broken",
				c.sel, c.val, got)
		}
	}
}

// TestAnalogBus_ADCRespectsMuxChannel verifies the sel=0x9F ADC result
// changes when the firmware switches mux channels via control reg B
// (sel=0x91). The exact values are heuristic — what matters is that
// distinct channels give distinct readings so cal-sweep paths that
// programme the mux see a coherent response curve.
func TestAnalogBus_ADCRespectsMuxChannel(t *testing.T) {
	var a analogBus
	// readADC selects a mux channel, triggers a conversion (DAC-low write),
	// then reads the latched result from 0x9F. The result is sampled at the
	// trigger, so the channel/DAC must be set before triggering.
	readADC := func(ch uint16, dac uint16) uint16 {
		a.writeSelect(abSelCtrlB)
		a.writeData(ch)
		a.writeSelect(abSelDACHi)
		a.writeData(0)
		a.writeSelect(abSelDACMid)
		a.writeData(0)
		a.writeSelect(abSelDACLo)
		a.writeData(dac) // triggers the conversion (latches the sample)
		a.writeSelect(abSelADC)
		return a.readData()
	}
	if v := readADC(0, 0); v != 0 { // ch0 CRD_ANLG_2
		t.Errorf("ch0 read = %#04X, want 0", v)
	}
	if v := readADC(2, 0); v != 0x100 { // ch2 +2VREF
		t.Errorf("ch2 read = %#04X, want 0x100", v)
	}
	if v := readADC(5, 0x42); v != 0x42 { // ch5 linear-DAC mode
		t.Errorf("ch5 with DAC=0x42: ADC = %#04X, want 0x42", v)
	}
}

// TestAnalogBus_ADCInFirmwareRange verifies the ADC response always
// fits the firmware's sanity-check window of [-0x200, +0x1FF] (the
// `cmpi.w` bounds at PC 0x5EF96 / 0x5EFA6 inside fcn.5EFAE cal-init).
// If the model ever returns out-of-range the cal-init fast-fails.
func TestAnalogBus_ADCInFirmwareRange(t *testing.T) {
	var a analogBus
	for ch := 0; ch < 8; ch++ {
		a.writeSelect(abSelCtrlB)
		a.writeData(uint16(ch))
		// Walk the DAC across a few values per channel.
		for _, dac := range []uint16{0x00, 0x55, 0xAA, 0xFF} {
			a.writeSelect(abSelDACLo)
			a.writeData(dac)
			a.writeSelect(abSelADC)
			raw := a.readData()
			// Interpret as 13-bit signed.
			v := int16(raw)
			if raw&0x1000 != 0 {
				v = int16(raw | 0xE000) // sign-extend from 13 bits
			}
			if v > 0x1FF || v < -0x200 {
				t.Errorf("ch=%d dac=0x%02X → ADC=%d (raw=%#04X), out of [-0x200, +0x1FF]",
					ch, dac, v, raw)
			}
		}
	}
}
