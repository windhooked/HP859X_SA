package device

import "testing"

// TestAnalogBus_StatusCadence verifies the sel=0x9A status register
// returns 0x0006 every Nth read and 0 otherwise — the cadence the
// firmware's wait_for_adc_match polls rely on.
func TestAnalogBus_StatusCadence(t *testing.T) {
	var a analogBus
	a.writeSelect(abSelStatus)
	matches := 0
	for i := 0; i < statusMatchEveryNReads*4; i++ {
		v := a.readData()
		if v == 0x0006 {
			matches++
		} else if v != 0 {
			t.Errorf("read #%d = %#04X, want 0x0006 or 0", i, v)
		}
	}
	if matches != 4 {
		t.Errorf("matches=%d in %d reads, want 4", matches, statusMatchEveryNReads*4)
	}
}

// TestAnalogBus_StatusMatchValueSatisfiesFirmwarePolls verifies the
// returned match value 0x06 satisfies BOTH firmware tests against the
// status low byte: `(0x12 & x) == 0x02` (operating loop at PC 0x5E5FA)
// and `x == 0x06` (init/cal stage at PC 0x5E708).
func TestAnalogBus_StatusMatchValueSatisfiesFirmwarePolls(t *testing.T) {
	const matchVal = 0x0006
	if (0x12 & matchVal) != 0x02 {
		t.Errorf("op-loop poll: (0x12 & 0x%02X) = 0x%02X, want 0x02",
			matchVal, 0x12&matchVal)
	}
	if matchVal&0xFF != 0x06 {
		t.Errorf("init-poll: low byte = 0x%02X, want 0x06", matchVal&0xFF)
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
	// Channel 0 (CRD_ANLG_2): expect 0.
	a.writeSelect(abSelCtrlB)
	a.writeData(0x0000)
	a.writeSelect(abSelADC)
	if v := a.readData(); v != 0 {
		t.Errorf("ch0 read = %#04X, want 0", v)
	}
	// Channel 2 (+2VREF): expect ~+0x100.
	a.writeSelect(abSelCtrlB)
	a.writeData(0x0002)
	a.writeSelect(abSelADC)
	if v := a.readData(); v != 0x100 {
		t.Errorf("ch2 read = %#04X, want 0x100", v)
	}
	// Channel 5 (linear-DAC mode): set DAC, expect ADC tracks low 9 bits.
	a.writeSelect(abSelCtrlB)
	a.writeData(0x0005)
	a.writeSelect(abSelDACHi)
	a.writeData(0)
	a.writeSelect(abSelDACMid)
	a.writeData(0)
	a.writeSelect(abSelDACLo)
	a.writeData(0x42)
	a.writeSelect(abSelADC)
	if v := a.readData(); v != 0x42 {
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
