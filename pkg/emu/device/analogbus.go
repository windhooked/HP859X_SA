package device

// analogBus models the A16 board's analog-control hybrid — the chip(s)
// behind the indirect register-pair at 0xFFF75C (select) and 0xFFF75E
// (data). Per CLIP 5963-2591 the physical components are:
//
//   - U47   12-bit ADC (the digitiser at the heart of every measurement)
//   - U64 + U201   8-channel analog mux (selects the ADC input channel:
//     CRD_ANLG_2 / VIDEO_IF / +2VREF / ACOM and four others)
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
//	0x9A — ADC status register (READ). Driven by a conversion state machine
//	       (see the convState fields + readData, and docs/ANALOG_BUS_MODEL.md).
//	       Low-byte bits: bit0 EOC/data-ready, bit1 ready, bit2 settled ⇒ 0x06
//	       idle, 0x07 data-ready. The firmware's polls have CONFLICTING
//	       contracts — the init poll wants exactly 0x06 (EOC clear) while the
//	       conversion-done polls want bit0 set — so no single constant works;
//	       the EOC bit sets after a triggered conversion and clears on the
//	       result read. Status is presented only every statusReadyEveryNReads
//	       reads ("busy" between) to keep the operating loop's background
//	       redraw alive.
//
//	0x9F / 0x9D — ADC result (READ). 0x9F is the 12-bit signed result
//	       (range-checked at PC 0x5EF96 against [-0x200, +0x1FF]); 0x9D is the
//	       coarse/sign byte fcn.5E6BC sign-extends into the high word. The
//	       result is latched when a conversion is triggered (the 0x97 DAC-low
//	       write) and a read consumes it (clears EOC → idle).
//
//	0x95 / 0x96 / 0x97 — 24-bit DAC bytes (WRITE-only).
//	       The firmware composes a 24-bit DAC word by writing
//	       sel=0x95 ⇒ bits[23:16] (high)
//	       sel=0x96 ⇒ bits[15:8]  (mid)
//	       sel=0x97 ⇒ bits[7:0]   (low)
//	       via fcn.5E384. We store each byte in a 24-bit register so
//	       subsequent ADC reads can be correlated with the DAC value.
//
//	0x90 / 0x91 / 0x93 — control registers (WRITE). Observed initial
//	       values (0x00 / 0x12 / 0x0F) suggest channel-select bits +
//	       mode/enable flags but we don't decode them per-bit yet —
//	       stored as a flat register file.
//
//	0x20             — one-shot init pulse (WRITE), exact function
//	                    unknown; stored.
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

	// U47 ADC conversion state machine (drives the sel=0x9A status register
	// and the sel=0x9F/0x9D result reads). See docs/ANALOG_BUS_MODEL.md.
	// The firmware's read sequence is: program the channel/DAC (the
	// send_dac_word writes to selects 0x95/0x96/0x97) to TRIGGER a
	// conversion, poll 0x9A until the EOC ("data ready") status bit sets,
	// then read the result from 0x9F (which consumes it). One constant
	// cannot satisfy the firmware's conflicting 0x9A poll contracts (the
	// init poll wants 0x06 with EOC clear; the conversion-done polls want
	// EOC set) — hence this state machine.
	convState     convPhase // idle → converting → done → (result read) → idle
	convReadCount int       // sel=0x9A status reads since the last trigger
	latchedADC    int16     // ADC sample taken at trigger; returned on 0x9F/0x9D
	donePresented bool      // EOC was shown on a pulse; decays to idle if unread

	// statusReadCount is the running count of sel=0x9A status reads; it
	// drives the "ready pulse" cadence (see statusReadyEveryNReads) that
	// keeps the operating loop's background-redraw work alive.
	statusReadCount uint64
}

// convPhase is the U47 ADC conversion lifecycle.
type convPhase uint8

const (
	convIdle       convPhase = iota // no conversion pending; status = 0x06
	convConverting                  // triggered, not yet complete; status = 0x06
	convDone                        // complete, result unread; status = 0x07 on pulse
)

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
	abSelStatus    = 0x9A // ADC status register — read-only (see status bits below)
	abSelADCHi     = 0x9D // ADC result coarse/sign byte — read-only (consumes result)
	abSelADC       = 0x9F // 12-bit signed ADC result — read-only (consumes result)
)

// sel=0x9A status low-byte bits. No datasheet defines these; they are
// derived from the firmware's poll contracts (docs/ANALOG_BUS_MODEL.md §5):
// the init poll waits for exactly 0x06 (EOC clear), the conversion-done
// polls wait for (mask & x) with bit 0 set.
const (
	adcStatusEOC     = 0x01                              // conversion complete / data ready
	adcStatusReady   = 0x02                              // hybrid powered/ready
	adcStatusSettled = 0x04                              // settled / not mid-conversion
	adcStatusIdle    = adcStatusReady | adcStatusSettled // 0x06 — ready, no pending data
)

// statusReadyEveryNReads: a sel=0x9A read presents the status value only
// every Nth read; between pulses it reads 0x00 ("busy"). This preserves the
// firmware's background-redraw cadence in the operating loop — returning
// "ready" on EVERY read collapses the render (see CLAUDE.md). 256 carries
// over the previous calibration.
const statusReadyEveryNReads = 256

// convReadsToEOC: a triggered conversion completes after this many sel=0x9A
// status reads (models the U47 conversion time). Kept well below the pulse
// period so a conversion is always finished by the time the next ready pulse
// can present its EOC bit.
const convReadsToEOC = 8

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
		// Writing the low DAC byte completes a send_dac_word (fcn.5E384) and
		// triggers an ADC conversion on the currently-selected mux channel.
		a.triggerConversion()
	}
}

// triggerConversion latches an ADC sample for the current mux channel and
// starts the conversion timer. It also clears any stale EOC (a new
// conversion supersedes an unread result).
func (a *analogBus) triggerConversion() {
	a.latchedADC = a.sampleADC()
	a.convState = convConverting
	a.convReadCount = 0
	a.donePresented = false
}

// sampleADC returns the 12-bit signed ADC reading for the current mux channel
// (selected via control reg 0x91 low bits) and DAC value. Per the service
// guide the ADC maps 0–2 V to bottom→top graticule; we return per-channel
// values inside the firmware's ±0x200 sanity band (range-checked at ROM
// 0x5EF96/0x5EFA6). See docs/ANALOG_BUS_MODEL.md §6.
func (a *analogBus) sampleADC() int16 {
	ch := a.regs[abSelCtrlB] & 0x07
	switch ch {
	case 0: // CRD_ANLG_2 — card-cage analog (centred)
		return 0
	case 1: // VIDEO_IF — small positive noise floor
		return 32
	case 2: // +2VREF — near top of scale
		return 0x100
	default:
		// Linear from DAC LSBs, sign-extended from 9 bits, so a cal sweep
		// that programs the DAC and reads back sees a coherent response.
		v := int16(a.dac & 0x1FF)
		if v&0x100 != 0 {
			v |= ^int16(0x1FF)
		}
		return v
	}
}

// readData dispatches the read on 0xFFF75E based on the current select.
func (a *analogBus) readData() uint16 {
	switch a.sel & 0xFF {
	case abSelStatus:
		// Advance the conversion timer on each status read; a converting
		// sample becomes "done" after convReadsToEOC reads.
		if a.convState == convConverting {
			a.convReadCount++
			if a.convReadCount >= convReadsToEOC {
				a.convState = convDone
			}
		}
		// Ready-pulse cadence: present the status only every Nth read, "busy"
		// (0x00) otherwise, so the firmware keeps doing background work
		// between ready events.
		a.statusReadCount++
		if a.statusReadCount%statusReadyEveryNReads != 0 {
			return 0x0000
		}
		// On a ready pulse: present 0x06 (idle) or 0x07 (data ready). EOC is a
		// transient — it is shown on exactly one pulse; if the firmware does
		// not read the result before the next pulse, the converter returns to
		// idle and EOC self-clears (so the init poll that waits for *exactly*
		// 0x06 is not blocked by a stale, unlatched conversion). An actively
		// waiting conversion-done poll catches that single 0x07 pulse and
		// reads the result (clearing EOC) well before the next pulse.
		s := uint16(adcStatusIdle) // 0x06: ready + settled
		if a.convState == convDone {
			if a.donePresented {
				a.convState = convIdle
				a.donePresented = false
			} else {
				a.donePresented = true
				s |= adcStatusEOC // 0x07: data ready
			}
		}
		return s
	case abSelADC, abSelADCHi:
		// Reading a result register returns the latched sample and consumes
		// the conversion (clears EOC → idle). 0x9D is the coarse/sign byte
		// that fcn.5E6BC sign-extends into the high word of a 32-bit reading;
		// returning 0 keeps the combined value equal to the 0x9F word.
		a.convState = convIdle
		a.donePresented = false
		if a.sel&0xFF == abSelADCHi {
			return 0x0000
		}
		return uint16(a.latchedADC) & 0x1FFF
	default:
		// Register-file passthrough for every other select: read what was
		// last written. Defaults to 0 for any select the firmware reads
		// without first writing — same behaviour as a freshly-powered chip
		// whose internal latches read back zero.
		return a.regs[a.sel&0xFF]
	}
}
