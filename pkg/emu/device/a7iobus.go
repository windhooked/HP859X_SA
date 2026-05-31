package device

import "os"

// A7ReadHist is a diagnostic histogram of A7-register reads: reg -> {count,
// last-select, last-returned}. Populated only when the A7_LOG env var is set.
// Used by cmd/longrun to find which A7 status registers the boot-time analog
// self-test polls (for the REF UNLOCK / OVEN COLD / ADC annunciators).
var A7ReadHist = map[int][3]int{}

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
// NOT YET MODELLED: register-specific READ semantics. Some selected registers
// are status/readback ports on real hardware (e.g. an A7/A25 settle-or-lock
// status, the Counterlock frequency value) rather than DAC read-back latches.
// The post-boot measurement freeze (docs/TRACE_DISPLAY_PATH.md) polls A7
// register 3, but its loop branches on IRQ-set RAM flags ($b1e0/$b212/$b213/
// $bf26), NOT on this readback — so a faithful register file is correct here
// and the freeze is gated by the sweep-event IRQ handshake, a separate task.
// When that handshake is modelled, any register found to need live status
// (lock/settle/counter) gets a case in readData, mirroring analogBus's 0x9A.
type a7IOBus struct {
	// Most-recent select word written to 0xFFF728. Bits [11:8] = register
	// address; bits [15:12] = control/mode (from the firmware's $AD7C shadow).
	sel uint16

	// 16-entry register file indexed by (sel >> 8) & 0x0F. Holds the last word
	// written via 0xFFF72A for each A7 register; reads return the stored value.
	regs [16]uint16
}

// reg returns the currently-addressed A7 register index (select bits [11:8]).
func (a *a7IOBus) reg() int { return int((a.sel >> 8) & 0x0F) }

// writeSelect latches the select word written to 0xFFF728.
func (a *a7IOBus) writeSelect(v uint16) { a.sel = v }

// writeData stores a word written to 0xFFF72A into the selected register.
func (a *a7IOBus) writeData(v uint16) { a.regs[a.reg()] = v }

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
