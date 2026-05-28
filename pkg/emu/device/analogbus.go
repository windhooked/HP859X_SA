package device

// analogBus models the A16 board's analog-control hybrid — the chip(s)
// behind the indirect register-pair at 0xFFF75C (select) and 0xFFF75E
// (data). Per CLIP 5963-2591 the physical components are:
//
//   - U47   12-bit ADC (the digitiser at the heart of every measurement)
//   - U64 + U201   8-channel analog mux (selects the ADC input channel:
//                  CRD_ANLG_2 / VIDEO_IF / +2VREF / ACOM and four others)
//   - one or more DACs that drive YIG-tune, LO-trim, and similar analog
//     control inputs; the firmware programs them via the 24-bit byte
//     stream split across selects 0x95 / 0x96 / 0x97
//
// Host protocol: the firmware writes a SELECT value to 0xFFF75C, then
// reads / writes the addressed register through 0xFFF75E. The model below
// captures the SELECT and dispatches reads / writes based on it, so each
// select can have its own semantics (status, ADC, DAC byte, control reg).
//
// Selects observed via cmd/abusprobe (Rev L boot + 100M op-loop cycles);
// see docs/rom_annotations.md "A16 analog-bus select map" for the full
// table. The ones with semantic effect in our model are:
//
//   0x9A — ADC-ready / status register (READ).
//          Bit-mapped flags polled by fcn.5E5DE wait_for_adc_match. The
//          firmware tests `(mask & low_byte) == target` with several
//          (mask, target) pairs; returning 0x0006 periodically satisfies
//          the dominant operating-loop poll (mask=0x12, target=0x02 ⇒
//          0x12 & 0x06 = 0x02) and the init/cal stage at 0x5E708 that
//          wants `low_byte == 0x06`. We keep the periodic-match cadence
//          rather than always-match so the firmware's background work
//          (annunciator redraw, etc.) gets cycles between ready events.
//
//   0x9F — 12-bit signed ADC result register (READ).
//          Range-checked at PC 0x5EF96 against [-0x200, +0x1FF]. Not read
//          in our current operating loop (the cal-sweep code at fcn.5EFAE
//          that would use it never executes). Modelled as a stored value
//          so a future model can correlate it with DAC writes.
//
//   0x95 / 0x96 / 0x97 — 24-bit DAC bytes (WRITE-only).
//          The firmware composes a 24-bit DAC word by writing
//          sel=0x95 ⇒ bits[23:16] (high)
//          sel=0x96 ⇒ bits[15:8]  (mid)
//          sel=0x97 ⇒ bits[7:0]   (low)
//          via fcn.5E384. We store each byte in a 24-bit register so
//          subsequent ADC reads can be correlated with the DAC value.
//
//   0x90 / 0x91 / 0x93 — control registers (WRITE). Observed initial
//          values (0x00 / 0x12 / 0x0F) suggest channel-select bits +
//          mode/enable flags but we don't decode them per-bit yet —
//          stored as a flat register file.
//
//   0x20             — one-shot init pulse (WRITE), exact function
//                       unknown; stored.
//
// Any select not listed gets register-file behaviour: writes store the
// value, reads return what was last stored. That gives consistent state
// across firmware paths even where we don't know the chip-side
// semantics yet — far better than the previous "drop on the floor"
// behaviour.
type analogBus struct {
	// Most-recently written select (low 8 bits used for the register-file
	// lookup; the high byte is preserved in the raw `sel` field in case a
	// future select uses it).
	sel uint16

	// 256-entry register file indexed by `sel & 0xFF`. Holds the last word
	// written via 0xFFF75E for every select the firmware uses. Reads of
	// 0xFFF75E for non-special selects return this stored value.
	regs [256]uint16

	// Decomposed 24-bit DAC value mirrored from regs[0x95..0x97] for
	// convenience (the cal-sweep code's "send DAC word" pattern uses all
	// three bytes as a single signed quantity).
	dac uint32

	// 12-bit signed ADC sample returned for sel=0x9F. Initialised to 0
	// (mid-scale; well within the firmware's ±0x200 sanity band). A
	// physical-fidelity follow-up can derive this from the DAC value or
	// from the current mux channel selected by the control-register
	// writes.
	adcResult int16

	// Status-register cadence for sel=0x9A. We arm a "ready" return
	// (0x0006) every statusMatchEveryNReads reads to mimic the
	// occasionally-ready behaviour of a real ADC + sample-and-hold,
	// preserving the firmware's existing background-work cadence.
	statusReadCount uint64
	statusPending   bool
}

// Symbolic select IDs. Names follow the CLIP 5963-2591 register naming
// where possible; otherwise they describe the observed firmware usage.
const (
	abSelInitPulse = 0x20 // one-shot init
	abSelCtrlA     = 0x90 // control reg A (observed init = 0x0000)
	abSelCtrlB     = 0x91 // control reg B (observed init = 0x0012; likely mux channel)
	abSelCtrlC     = 0x93 // control reg C (observed init = 0x000F; likely ADC mode bits)
	abSelDACHi     = 0x95 // DAC byte [23:16]
	abSelDACMid    = 0x96 // DAC byte [15:8]
	abSelDACLo     = 0x97 // DAC byte [7:0]
	abSelStatus    = 0x9A // ADC-ready status — read-only
	abSelADC       = 0x9F // 12-bit signed ADC result — read-only
)

// statusMatchEveryNReads sets how often a sel=0x9A read returns the
// match value (0x0006). 256 is the calibration that keeps the firmware's
// annunciator-redraw work visible — see indirectMatchEveryNReads (now
// removed) for the original derivation.
const statusMatchEveryNReads = 256

// writeSelect captures the select value written to 0xFFF75C.
func (a *analogBus) writeSelect(sel uint16) { a.sel = sel }

// writeData stores a word written to 0xFFF75E into the register file and
// updates any decomposed mirrors (the 24-bit DAC). Selects 0x9A and 0x9F
// are read-only on real hardware — we still store any spurious writes in
// the regs slot so a misbehaving firmware path doesn't drop state.
func (a *analogBus) writeData(val uint16) {
	a.regs[a.sel&0xFF] = val
	switch a.sel & 0xFF {
	case abSelDACHi:
		a.dac = (a.dac &^ 0xFF0000) | (uint32(val)&0xFF)<<16
	case abSelDACMid:
		a.dac = (a.dac &^ 0xFF00) | (uint32(val)&0xFF)<<8
	case abSelDACLo:
		a.dac = (a.dac &^ 0xFF) | (uint32(val) & 0xFF)
	}
}

// readData dispatches the read on 0xFFF75E based on the current select.
func (a *analogBus) readData() uint16 {
	switch a.sel & 0xFF {
	case abSelStatus:
		// Periodic "ready" pulse: 0x0006 every statusMatchEveryNReads reads.
		// 0x0006 satisfies both observed firmware tests against the low byte
		// (`(0x12 & x) == 0x02` and `x == 0x06`).
		a.statusReadCount++
		arm := a.statusReadCount%statusMatchEveryNReads == 0
		if arm || a.statusPending {
			a.statusPending = false
			return 0x0006
		}
		return 0
	case abSelADC:
		// 12-bit signed ADC result. Derived from the current state of the
		// chip's input mux (selected via control-reg writes 0x90/0x91/0x93)
		// and the most-recent DAC value, so the firmware's cal sweeps see
		// a coherent response curve rather than a flat zero.
		//
		// Approximation, calibrated to firmware expectations:
		//   - mux channel inferred from regs[0x91] bits [2:0] (per the
		//     observed init value 0x0012 the firmware seems to write
		//     channel-id + enable here; if a real CLIP page proves a
		//     different bit-layout, this is the place to fix it).
		//   - channel 0 = CRD_ANLG_2: returns 0 (centred analog ground)
		//   - channel 1 = VIDEO_IF: returns a small positive noise-floor
		//     reading (~+32, well below the 0x1FF clamp)
		//   - channel 2 = +2VREF: returns ~+0x100 (the firmware's "+2V"
		//     reference, scaled into ADC counts)
		//   - other channels: track the DAC value (lower 9 bits) so a
		//     cal sweep that programs the DAC and reads back sees a
		//     linear response — enough to pass `bgt 0x1FF` / `blt -0x200`
		//     bounds checks but not so high it pegs the ADC.
		ch := a.regs[abSelCtrlB] & 0x07
		var adc int16
		switch ch {
		case 0:
			adc = 0
		case 1:
			adc = 32
		case 2:
			adc = 0x100
		default:
			// Linear from DAC LSBs, sign-extended from 9 bits.
			v := int16(a.dac & 0x1FF)
			if v&0x100 != 0 {
				v |= ^int16(0x1FF)
			}
			adc = v
		}
		return uint16(adc) & 0x1FFF
	default:
		// Register-file passthrough for every other select: read what was
		// last written. Defaults to 0 for any select the firmware reads
		// without first writing — same behaviour as a freshly-powered chip
		// whose internal latches read back zero.
		return a.regs[a.sel&0xFF]
	}
}
