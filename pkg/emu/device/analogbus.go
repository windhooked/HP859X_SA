package device

import (
	"fmt"
	"os"
	"strconv"
)

// adcDebugN, when >0, logs the next N ADC conversion triggers (set via env
// ADC_DEBUG=<count>) — a temporary diagnostic for reverse-engineering the
// PRESET ADC cal's expected transfer function.
var adcDebugN = func() int {
	n, _ := strconv.Atoi(os.Getenv("ADC_DEBUG"))
	return n
}()

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
//	       result read. The ready/settled bits (0x06) are STATIC (present on
//	       every read while powered); only EOC is transient.
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
	// EOC set) — hence this state machine. The ready/settled bits (0x06) are
	// STATIC (asserted on every read while powered, like the real hybrid);
	// only EOC (bit0) is transient — set when a conversion completes, cleared
	// when the result is read (or self-cleared if the result is never read,
	// so an init poll waiting for exactly 0x06 is never permanently blocked).
	convState     convPhase // idle → converting → done → (result read) → idle
	convReadCount int       // sel=0x9A status reads since entering the current phase
	latchedADC    int16     // ADC sample taken at trigger; returned on 0x9F/0x9D

	// videoSample, when set, supplies the detector video sample for the
	// CPU-polled sweep path: a 0x9F result read returns (sample*2 - 0x200)
	// (the signed-12-bit video mapping) whenever the hook reports ok — i.e.
	// a sweep is active and the PC is inside the firmware's polled-sweep
	// loops (ROM 0x5f88x). Wired by machine.New8593A via SetVideoSample.
	videoSample func() (uint16, bool)
}

// convPhase is the U47 ADC conversion lifecycle.
type convPhase uint8

const (
	convIdle       convPhase = iota // no conversion pending; status = 0x06
	convConverting                  // triggered, not yet complete; status = 0x06
	convDone                        // complete, result unread; status = 0x07 (EOC set)
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

// Static status, no cadence: the sel=0x9A status presents the ready/settled
// bits (0x06) on EVERY read — that is faithful to the real U47 hybrid (those
// bits are asserted continuously while powered). The previous model returned
// 0x00 ("busy") on 255 of every 256 reads; that was unfaithful (it momentarily
// claimed the hybrid unpowered, bit1 clear) and it TRAPPED the boot-measurement
// analog poll fcn.5e5de — which waits for (status & 0x12)==0x02 with a ~1000-
// unit timeout — into running every wait to full timeout, starving the
// operating loop ~8×. The feared "ready-every-read collapses the render"
// regression does NOT occur with this conversion state machine (measured: the
// vector count is unchanged — see the cadence A/B in oploop_diag_test.go's
// history and docs/TRACE_DISPLAY_PATH.md).

// convReadsToEOC: a triggered conversion completes (EOC asserts) after this
// many sel=0x9A status reads — models the U47 conversion time so a poll that
// triggers then immediately checks EOC sees "still converting" briefly first.
const convReadsToEOC = 8

// staleReadsToIdle: if a completed conversion's result is never read (e.g. an
// init poll that waits for exactly 0x06 without reading 0x9F), EOC self-clears
// back to idle after this many sel=0x9A reads, so the init poll is never
// permanently blocked. A real conversion-done poll reads the result on its very
// first status read (EOC is now static), so this only governs the no-reader case.
const staleReadsToIdle = 64

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
		if adcDebugN > 0 {
			adcDebugN--
			fmt.Fprintf(os.Stderr, "ADC trig: ch=%d ctlA(90)=%04X ctlB(91)=%04X ctlC(93)=%04X dac=%06X -> adc=%d(%#04x)\n",
				a.regs[abSelCtrlB]&0x07, a.regs[abSelCtrlA], a.regs[abSelCtrlB], a.regs[abSelCtrlC],
				a.dac, a.latchedADC, uint16(a.latchedADC))
		}
	}
}

// triggerConversion latches an ADC sample for the current mux channel and
// starts the conversion timer. It also clears any stale EOC (a new
// conversion supersedes an unread result).
func (a *analogBus) triggerConversion() {
	a.latchedADC = a.sampleADC()
	a.convState = convConverting
	a.convReadCount = 0
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
	case 2: // +2VREF — near top of scale.
		// OPEN TASK: the PRESET ADC cal (fcn.5E6E8) is a successive-
		// approximation cal that sweeps the offset DAC (logged via ADC_DEBUG)
		// and measures this channel at DAC≈0x00 vs ≈0x90, expecting a specific
		// gain/offset. A constant (zero slope) and linear-null guesses
		// (0x180-dac, 0x1C0-3·dac) both leave the cal cycling ADC-TIME/GND/2V
		// FAIL — it needs the EXACT U47 transfer function the cal validates
		// (range-checks @0x5EF96, result compare @0x5E822). Cosmetic per
		// docs/ANNUNCIATORS.md, but the trace's dB scaling depends on it.
		// Best cracked with the real-instrument GPIB oracle for ground-truth
		// ADC readings. See the analog-bus memory note.
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
		// Static ready/settled bits (0x06) on EVERY read — faithful to the
		// powered hybrid; EOC (bit0) is the only transient. The "busy 0x00"
		// cadence was removed (it trapped fcn.5e5de's timeout poll — see the
		// note above triggerConversion).
		s := uint16(adcStatusIdle) // 0x06: ready + settled
		switch a.convState {
		case convConverting:
			// Conversion in flight: EOC still clear (0x06). Completes after
			// convReadsToEOC status reads.
			a.convReadCount++
			if a.convReadCount >= convReadsToEOC {
				a.convState = convDone
				a.convReadCount = 0
			}
		case convDone:
			// Result ready: assert EOC (0x07) until the result read consumes it
			// (abSelADC/abSelADCHi → convIdle). If no read ever comes, self-clear
			// after staleReadsToIdle so an init poll waiting for exactly 0x06 is
			// not permanently blocked.
			s |= adcStatusEOC // 0x07: data ready
			a.convReadCount++
			if a.convReadCount >= staleReadsToIdle {
				a.convState = convIdle
				a.convReadCount = 0
			}
		}
		return s
	case abSelADC, abSelADCHi:
		// Reading a result register returns the latched sample and consumes
		// the conversion (clears EOC → idle). 0x9D is the coarse/sign byte
		// that fcn.5E6BC sign-extends into the high word of a 32-bit reading;
		// returning 0 keeps the combined value equal to the 0x9F word.
		a.convState = convIdle
		a.convReadCount = 0
		if a.sel&0xFF == abSelADCHi {
			return 0x0000
		}
		// CPU-polled sweep path (★ 2026-07-13): during an active sweep the
		// firmware's polled sweep loops (ROM 0x5f88x, pos/neg/sample detect)
		// read 0x9F per sample expecting the mux to be on the VIDEO channel —
		// the transform is (result + 0x200) << 2, i.e. a signed 12-bit sample
		// in [-0x200, 0x1FF]. When the videoSample hook is armed (sweep active
		// + PC in the polled-sweep region — see machine.New8593A), return the
		// detector sample mapped into that range instead of the cal-path latch.
		if a.videoSample != nil {
			if v, ok := a.videoSample(); ok {
				return uint16(int32(v)*2-0x200) & 0xFFFF
			}
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
