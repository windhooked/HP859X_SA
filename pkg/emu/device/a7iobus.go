package device

import "os"

// A7ReadHist / A7WriteHist are diagnostic histograms of A7-register accesses:
// reg -> {count, last-select-or-?, last-value}. Populated only when the A7_LOG
// env var is set. Used to recover the A7 register map (which registers are the
// YTO/span tuning DACs — 12-bit ramping values — vs control/status).
var A7ReadHist = map[int][3]int{}
var A7WriteHist = map[int][3]int{} // reg -> {count, minVal, maxVal}

var a7LogOn = os.Getenv("A7_LOG") != ""

// a7IOBus models the A16→A7 "I/O bus": the indirect register pair at
// 0xFFF728 (select) and 0xFFF72A (data) through which the A16 processor board
// programs the A7 analog-interface assembly and reads its status back.
//
// Subsystem (see docs/A7_ANALOG_IO_BUS.md, derived from service guide
// 08590-90316 Ch.5/9/14 + firmware driver 0x223CC–0x22660): the A7 board
// produces the analog control signals for most of the analyzer — YTO/LO tune
// DACs, band switching, the sweep ramp, the reference-level DAC, the bandwidth
// companding DACs, and the A12/A14 cal-attenuator + step-gain switching — and
// returns status plus the A25 Counterlock LO/IF frequency-counter readings.
// This is a SEPARATE interface from the 0xFFF75C/75E analog-control hybrid
// (analogBus), which is the on-A16 ADC-input mux + 12-bit ADC. 75C/75E reads
// digitised video/reference; 728/72A controls A7 and reads its status.
//
// Host protocol (firmware fcn.22532 write / fcn.223be nibble-DAC loader /
// fcn.22646 read): write a SELECT word to 0xFFF728, then read or write the
// addressed register via 0xFFF72A. The select word is composed as
//
//	(reg_addr << 8 & 0x0FFF) | (shadow $AD7C & 0xF000)
//
// so the addressed A7 register is bits [11:8] (16 registers) and the top
// nibble [15:12] carries control/mode bits the firmware keeps in the RAM
// shadow at 0x00AD7C. Wide DACs are loaded one 4-bit nibble at a time (the low
// 4 bits of successive writes); multi-byte readbacks re-read one selected
// register.
//
// Model: a 16-entry register file indexed by the select's [11:8] field. A
// write to 0xFFF72A stores into the selected register; a read returns what was
// last stored there. This replaces the previous behaviour where 0xFFF728/72A
// fell through to the flat MMIO byte buffer (so every read returned the single
// last word written to address 0x72A regardless of which register was
// selected — wrong, and the source of the constant 0x72E2 the post-boot
// measurement loop kept reading). Per-register latches match how the firmware
// programs-then-reads-back individual A7 control points.
//
// REGISTER SEMANTICS (★ 2026-07-12 map, docs/A7_ANALOG_IO_BUS.md — disasm-derived):
//   reg 0  W  YTO serial DAC chain — fcn.223b6 writes 8 nibbles (sub-index in
//             data bits [7:4], nibble in [3:0]): groups 2+3+3 = an 8-bit value
//             then two 12-bit values, LS-nibble first. Assembled below.
//   reg 2  W  multiplexed control-latch port: pointer byte (group<<6)|0x30|
//             (sub<<1) then 16-bit data as two byte writes; 0xE2 = settle
//             strobe (fcn.227f2, precedes the reg-3 poll); 0xE0|(2<<n) =
//             gain/measure-path select (fcn.2287e).
//   reg 3  R  status + 16-bit readback: settle gate (x&0xC0)==0x80; two
//             consecutive reads = hi/lo measurement bytes.
//   reg 4  W  latch, cleared to 0 at band-config end only.
//   reg 5  W  10 MHz TIMEBASE reference DAC (8-bit; cal NVRAM 0x2FC037).
//   reg 6  W  analog control/mode latch (band/lock-strobe/span-mode bits).
//   reg 7  R  status/ID: bit1 = YTO lock-error, bit3 = hardware variant
//             (b213.4: ÷3 vs ÷40 chain split). Register-file zero default =
//             no lock error, variant B — the values the working boot sees.
// Reg 3 stays FORCE-SETTLED (conservative — the always-settled gate is what
// un-froze the post-boot measurement loop; gating it on the 0xE2 strobe is a
// future fidelity step once the sweep-event IRQ handshake is modelled).
// NOTE the YTO coil DACs are NOT here: FM/fine/coarse are direct word ports
// 0xFFF700/702/704 (shadows B1A4/B1A6/B1A8) — see HP8593AMMIO.YTOCoilDACs.
type a7IOBus struct {
	// Most-recent select word written to 0xFFF728. Bits [11:8] = register
	// address; bits [15:12] = control/mode (from the firmware's $AD7C shadow).
	sel uint16

	// 16-entry register file indexed by (sel >> 8) & 0x0F. Holds the last word
	// written via 0xFFF72A for each A7 register; reads return the stored value.
	regs [16]uint16

	// chain holds the assembled reg-0 YTO serial DAC chain: [0] = the 8-bit
	// first group (nibbles 0–1), [1] = the 12-bit second group (nibbles 2–4),
	// [2] = the 12-bit third group (nibbles 5–7). For a reg-0 tune transaction
	// the firmware loads [remainder, quotient, 0x32] of the AD60 value split
	// by the variant divisor (÷3 or ÷40) — see fcn.223b6.
	chain [3]uint16

	// settleStrobes counts reg-2 ← 0xE2 writes (the settle strobe the firmware
	// issues before polling the reg-3 gate). Diagnostic/test telemetry.
	settleStrobes int
}

// reg returns the currently-addressed A7 register index (select bits [11:8]).
func (a *a7IOBus) reg() int { return int((a.sel >> 8) & 0x0F) }

// writeSelect latches the select word written to 0xFFF728.
func (a *a7IOBus) writeSelect(v uint16) { a.sel = v }

// writeData stores a word written to 0xFFF72A into the selected register.
// Reg 0 additionally assembles the nibble-chain protocol; reg 2 tracks the
// settle strobe. See the register-semantics comment on a7IOBus.
func (a *a7IOBus) writeData(v uint16) {
	a.regs[a.reg()] = v
	switch a.reg() {
	case 0:
		// YTO serial DAC chain nibble: data bits [7:4] = sub-index 0–7 (the
		// firmware's `addi.w #0x10` per nibble), bits [3:0] = the nibble,
		// LS-nibble first within each group (2+3+3 nibbles).
		idx := int(v>>4) & 0x07
		nib := v & 0x0F
		switch {
		case idx < 2: // group 0: 8-bit
			if idx == 0 {
				a.chain[0] = 0
			}
			a.chain[0] |= nib << (4 * idx)
		case idx < 5: // group 1: 12-bit
			if idx == 2 {
				a.chain[1] = 0
			}
			a.chain[1] |= nib << (4 * (idx - 2))
		default: // group 2: 12-bit
			if idx == 5 {
				a.chain[2] = 0
			}
			a.chain[2] |= nib << (4 * (idx - 5))
		}
	case 2:
		if v&0xFF == 0xE2 {
			a.settleStrobes++
		}
	}
	if a7LogOn {
		r := a.reg()
		h := A7WriteHist[r]
		if h[0] == 0 {
			h[1], h[2] = int(v), int(v)
		}
		if int(v) < h[1] {
			h[1] = int(v)
		}
		if int(v) > h[2] {
			h[2] = int(v)
		}
		h[0]++
		A7WriteHist[r] = h
	}
}

// a7Reg3SettledHi / a7Reg3SettledLo are the bit 7 / bit 6 status of A7
// register 3, the analog-settle/lock status the post-boot measurement loop
// polls. The firmware at ROM 0x22818 reads register 3 and spins until
// `(readback & 0xC0) == 0x80` — i.e. bit 7 SET, bit 6 CLEAR — then proceeds
// (writes command 0x203 to the $bffe mailbox). On real hardware bit 7 asserts
// once the A7 analog chain has settled / the LO has locked after the firmware
// programs it; bit 6 is a separate flag (a band/gain valid bit tested
// elsewhere at 0x228c2). We report "settled" so the measurement state machine
// advances past this poll. See docs/A7_ANALOG_IO_BUS.md.
const (
	a7Reg3Settled = 0x80 // bit7 = settled/locked, bit6 = 0
)

// readData returns the selected register's value. Register 3 is a live status
// register (the analog-settle/lock status — see a7Reg3Settled); every other
// register falls through to the register file (last-written value, 0 if
// untouched). Wide DAC/readback registers thus stay faithful while the one
// status register the firmware gates on reports ready.
func (a *a7IOBus) readData() uint16 {
	if a7LogOn {
		v := A7ReadHist[a.reg()]
		A7ReadHist[a.reg()] = [3]int{v[0] + 1, int(a.sel), int(a.regs[a.reg()])}
	}
	switch a.reg() {
	case 3:
		// Force bits 6–7 to the "settled" pattern (bit7=1, bit6=0); preserve any
		// other bits a caller might have stored so non-status readers see them.
		return (a.regs[3] &^ 0x00C0) | a7Reg3Settled
	default:
		return a.regs[a.reg()]
	}
}

// YTOChain returns the assembled reg-0 YTO serial DAC chain values: the 8-bit
// first group, then the two 12-bit groups (see the chain field). For a normal
// tune these are [AD60 % divisor, AD60 / divisor, 0x32] with divisor 3 or 40
// per the hardware-variant flag. Zero until the firmware first loads the chain.
func (a *a7IOBus) YTOChain() (g0, g1, g2 uint16) {
	return a.chain[0], a.chain[1], a.chain[2]
}

// TimebaseDAC returns the 8-bit 10 MHz timebase reference DAC value (A7 reg 5;
// firmware live shadow 0x9ED7, cal-NVRAM byte 0x2FC037). Zero until written.
func (a *a7IOBus) TimebaseDAC() uint8 { return uint8(a.regs[5]) }

// SettleStrobes returns how many reg-2 settle strobes (0xE2) the firmware has
// issued — each normally precedes a reg-3 settle-gate poll (fcn.227f2).
func (a *a7IOBus) SettleStrobes() int { return a.settleStrobes }
