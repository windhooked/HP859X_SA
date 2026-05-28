package device

// TMS9914A models a Texas Instruments TMS9914A IEEE-488 (HP-IB / GPIB)
// controller. The chip lives at HP 8593A MMIO offset 0x600..0x61F (32
// bytes, with the device's 8 read + 8 write registers accessed at a
// 2-byte stride so each is byte-addressable on the M68K's even byte lane).
//
// This is a MINIMAL model — its job is to stop the firmware's HP-IB
// init paths from spinning on missing chip responses, and to let the
// dispatcher's natural-event path (IRQ4 → fcn.1D58 → fcn.1B40 → operating
// tick) run end-to-end without the DriveOperatingTick workaround. The
// full chip behavior (talker/listener state machine, byte-transfer
// handshake timing, faithful IS0/IS1/ADSR/BSR status semantics) is NOT
// modelled.
//
// Register map (offset from chip base = MMIO offset 0x600):
//
//	off  read                              write
//	---  --------------------------------  --------------------------------
//	0x0  IS0   Interrupt Status 0          IMR0  Interrupt Mask 0
//	0x2  IS1   Interrupt Status 1          IMR1  Interrupt Mask 1
//	0x4  ADSR  Address Status              AUXCR Auxiliary Command
//	0x6  BSR   Bus Status                  ADR   Address
//	0x8  (unused on read)                  SPMR  Serial Poll Mode
//	0xA  CPTR  Command Pass Through        PPR   Parallel Poll
//	0xC  (unused)                          (unused)
//	0xE  DIR   Data In                     CDOR  Data Out
//
// The CHIP's IRQ output asserts whenever (IS0 & IMR0) != 0 OR
// (IS1 & IMR1) != 0. The host (M68K) sees this on autovector level 4.
//
// IS0 bits (per datasheet):
//
//	7 INT1  — interrupt from IS1
//	6 SRQ   — service request
//	5 MAC   — my address change
//	4 RLC   — remote-local change
//	3 SPAS  — serial poll active
//	2 END   — END message received
//	1 BO    — byte out (controller can send next byte)
//	0 BI    — byte in (received byte ready)
//
// IS1 bits (per datasheet):
//
//	7 GET   — group execute trigger
//	6 ERR   — handshake error
//	5 UNC   — unrecognized command
//	4 APT   — address pass-through
//	3 DCAS  — device clear active
//	2 MA    — my address
//	1 IFC   — interface clear
//	0 (unused / 0)
type TMS9914A struct {
	// Eight write-side registers. Indices are by chip register number
	// (0..7), each mapping to 2-byte-stride MMIO offsets 0x0, 0x2, 0x4...
	wregs [8]byte
	// Eight read-side registers. Some (like IS0/IS1) reflect device
	// status the chip generates; others (like BSR) are wired into bus
	// state the firmware programmed via the write side. For the minimal
	// model we initialise these to a "bus idle" snapshot so polling
	// loops see a quiescent bus.
	rregs [8]byte
}

// TMS9914 register offsets (from chip base; both read and write).
const (
	tms9914Reg0 = 0 // IS0 (R) / IMR0 (W)
	tms9914Reg1 = 1 // IS1 (R) / IMR1 (W)
	tms9914Reg2 = 2 // ADSR (R) / AUXCR (W)
	tms9914Reg3 = 3 // BSR (R) / ADR (W)
	tms9914Reg4 = 4 // (unused R) / SPMR (W)
	tms9914Reg5 = 5 // CPTR (R) / PPR (W)
	tms9914Reg6 = 6 // (unused) / (unused)
	tms9914Reg7 = 7 // DIR (R) / CDOR (W)
)

// IS0 status bits (read-side). Exported for external IRQ-trigger code
// (cmd/* probes, machine tests) that programmatically signals chip state.
const (
	TMS9914_IS0_INT1 = 1 << 7
	TMS9914_IS0_SRQ  = 1 << 6
	TMS9914_IS0_MAC  = 1 << 5
	TMS9914_IS0_RLC  = 1 << 4
	TMS9914_IS0_SPAS = 1 << 3
	TMS9914_IS0_END  = 1 << 2
	TMS9914_IS0_BO   = 1 << 1
	TMS9914_IS0_BI   = 1 << 0
)

// IS1 status bits (read-side).
const (
	TMS9914_IS1_GET  = 1 << 7
	TMS9914_IS1_ERR  = 1 << 6
	TMS9914_IS1_UNC  = 1 << 5
	TMS9914_IS1_APT  = 1 << 4
	TMS9914_IS1_DCAS = 1 << 3
	TMS9914_IS1_MA   = 1 << 2
	TMS9914_IS1_IFC  = 1 << 1
)

// Backward-compat aliases for the test file's internal use.
const (
	tms9914IS0_INT1 = TMS9914_IS0_INT1
	tms9914IS0_SRQ  = TMS9914_IS0_SRQ
	tms9914IS0_MAC  = TMS9914_IS0_MAC
	tms9914IS0_RLC  = TMS9914_IS0_RLC
	tms9914IS0_SPAS = TMS9914_IS0_SPAS
	tms9914IS0_END  = TMS9914_IS0_END
	tms9914IS0_BO   = TMS9914_IS0_BO
	tms9914IS0_BI   = TMS9914_IS0_BI

	tms9914IS1_GET  = TMS9914_IS1_GET
	tms9914IS1_ERR  = TMS9914_IS1_ERR
	tms9914IS1_UNC  = TMS9914_IS1_UNC
	tms9914IS1_APT  = TMS9914_IS1_APT
	tms9914IS1_DCAS = TMS9914_IS1_DCAS
	tms9914IS1_MA   = TMS9914_IS1_MA
	tms9914IS1_IFC  = TMS9914_IS1_IFC
)

// NewTMS9914A returns a chip in a bus-idle / no-interrupt state.
func NewTMS9914A() *TMS9914A {
	return &TMS9914A{}
}

// readRegOffset maps a byte address inside the device's 32-byte window
// to a chip register index (0..7), or -1 if the address is between
// registers (the chip ignores odd addresses inside its window) or
// outside the modelled range (regs above index 7).
func tms9914RegFromOffset(off uint32) int {
	if off&1 != 0 {
		return -1
	}
	idx := int(off >> 1)
	if idx >= 8 {
		return -1
	}
	return idx
}

// ReadByte returns the byte at the given chip-local offset (0..0x1F).
// Reads at odd offsets and at offsets above 0x0F return 0.
func (t *TMS9914A) ReadByte(off uint32) byte {
	idx := tms9914RegFromOffset(off)
	if idx < 0 {
		return 0
	}
	return t.rregs[idx]
}

// WriteByte stores `val` into the chip's write-side register selected
// by `off`. Writes at odd offsets and at offsets above 0x0F are dropped.
// Side effects: a write to AUXCR (offset 4) is interpreted as an
// auxiliary command — the minimal model handles the "Software Reset"
// (swrst) command which clears interrupt status, and otherwise stores
// the byte for inspection by future code. The full set of auxiliary
// commands (tcs, tca, gts, rtl, rhdf, lon, ton, dai, fget, rls, etc.)
// is not modelled.
func (t *TMS9914A) WriteByte(off uint32, val byte) {
	idx := tms9914RegFromOffset(off)
	if idx < 0 {
		return
	}
	t.wregs[idx] = val

	if idx == tms9914Reg2 { // AUXCR
		// AUXCR encoding: bit 7 = SET (1) / CLEAR (0); bits 0..4 = command.
		cmd := val & 0x1F
		set := val&0x80 != 0
		t.execAuxCmd(cmd, set)
	}
}

// execAuxCmd handles the subset of auxiliary commands needed for the
// minimal model. Currently only Software Reset (swrst, cmd=0) is honoured.
func (t *TMS9914A) execAuxCmd(cmd byte, set bool) {
	switch cmd {
	case 0x00: // swrst — Software Reset
		if set {
			// Reset internal state but preserve register values the
			// firmware uses for config (IMR0, IMR1, ADR, etc.).
			t.rregs[tms9914Reg0] = 0 // IS0 = no interrupts
			t.rregs[tms9914Reg1] = 0 // IS1 = no interrupts
		}
	}
}

// IRQAsserted reports whether the chip is currently driving its
// interrupt output line. The chip asserts whenever any masked status
// bit in IS0 or IS1 matches the corresponding mask in IMR0 or IMR1.
func (t *TMS9914A) IRQAsserted() bool {
	is0 := t.rregs[tms9914Reg0]
	imr0 := t.wregs[tms9914Reg0]
	is1 := t.rregs[tms9914Reg1]
	imr1 := t.wregs[tms9914Reg1]
	return (is0&imr0) != 0 || (is1&imr1) != 0
}

// SetIS0 sets the named IS0 status bits and updates IRQ state.
// Callers responsible for clearing bits when the firmware acks
// (via reads of IS0 — the chip auto-clears on read for some bits;
// this minimal model does NOT auto-clear).
func (t *TMS9914A) SetIS0(bits byte) {
	t.rregs[tms9914Reg0] |= bits
}

// SetIS1 sets the named IS1 status bits and updates IRQ state.
func (t *TMS9914A) SetIS1(bits byte) {
	t.rregs[tms9914Reg1] |= bits
}

// IS0 / IMR0 / IS1 / IMR1 / ADSR / AUXCR / BSR / ADR accessors for tests
// and the IRQ-trigger logic.
func (t *TMS9914A) IS0() byte  { return t.rregs[tms9914Reg0] }
func (t *TMS9914A) IS1() byte  { return t.rregs[tms9914Reg1] }
func (t *TMS9914A) IMR0() byte { return t.wregs[tms9914Reg0] }
func (t *TMS9914A) IMR1() byte { return t.wregs[tms9914Reg1] }
