package device

import (
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
)

// TestA7IOBus_RegisterFile checks the indirect select/data semantics: the
// register addressed by select bits [11:8] is written/read independently of
// the others, and an unwritten register reads back 0 (not a stale echo of a
// write to a different register — the bug the flat byte-buffer had).
func TestA7IOBus_RegisterFile(t *testing.T) {
	var a a7IOBus

	// Select reg 2 (mode nibble 0x1 in the top, as the firmware composes it),
	// write a value, read it back. Reg 2 is a plain register-file slot.
	a.writeSelect(0x1200)
	a.writeData(0xBEEF)
	if got := a.readData(); got != 0xBEEF {
		t.Fatalf("reg2 readback = %#04x, want 0xBEEF", got)
	}

	// A different register is independent and reads back 0 until written.
	a.writeSelect(0x0500) // reg 5
	if got := a.readData(); got != 0 {
		t.Fatalf("unwritten reg5 = %#04x, want 0", got)
	}
	a.writeData(0x1234)

	// Re-selecting reg 2 still returns its own value (no cross-talk).
	a.writeSelect(0x9200) // reg 2, different mode nibble
	if got := a.readData(); got != 0xBEEF {
		t.Fatalf("reg2 after touching reg5 = %#04x, want 0xBEEF", got)
	}
}

// TestA7IOBus_Reg3SettledStatus checks that A7 register 3 reports the
// analog-settled status the firmware gates on at ROM 0x22818: bits 6–7 must
// read as 0b10 (bit7 set, bit6 clear) so `(readback & 0xC0) == 0x80`, while
// other bits pass through any stored value.
func TestA7IOBus_Reg3SettledStatus(t *testing.T) {
	var a a7IOBus
	a.writeSelect(0x0300) // reg 3
	got := a.readData()
	if got&0xC0 != 0x80 {
		t.Fatalf("reg3 status bits = %#02x, want bit7 set + bit6 clear (0x80)", got&0xC0)
	}
}

// TestA7IOBus_ThroughMMIO checks the 0xFFF728/0xFFF72A wiring: a select word
// to 0xFFF728 then data to 0xFFF72A round-trips per register, and reads no
// longer return the stale flat-buffer echo across selects.
func TestA7IOBus_ThroughMMIO(t *testing.T) {
	m := NewHP8593AMMIO()

	// Program A7 register 2 with 0xA5A5.
	m.Write(0x728, bus.Word, 0x0200) // select reg 2
	m.Write(0x72A, bus.Word, 0xA5A5) // data
	if got := m.Read(0x72A, bus.Word); got != 0xA5A5 {
		t.Fatalf("reg2 via MMIO = %#04x, want 0xA5A5", got)
	}

	// Select reg 4 (never written) — must read 0, not the reg-2 value.
	m.Write(0x728, bus.Word, 0x0400)
	if got := m.Read(0x72A, bus.Word); got != 0 {
		t.Fatalf("unwritten reg4 via MMIO = %#04x, want 0 (no stale echo)", got)
	}
}

// TestA7IOBus_YTOChainAssembly drives the reg-0 nibble-chain protocol exactly
// as the firmware's fcn.223b6 does (8 writes, sub-index in data bits [7:4],
// LS-nibble first, groups 2+3+3) and checks the assembled group values.
// Example: AD60=1234, variant divisor 40 → chain = [34, 30, 0x32]
// (1234%40=34, 1234/40=30, third group constant 0x32).
func TestA7IOBus_YTOChainAssembly(t *testing.T) {
	var a a7IOBus
	load := func(idx int, val uint16) { // one fcn.223be nibble write
		w := uint16(idx)<<4 | (val & 0x0F)
		a.writeSelect(w) // select and data get the same composed word
		a.writeData(w)
	}
	chain := func(g0, g1, g2 uint16) { // full 2+3+3 transaction
		load(0, g0)
		load(1, g0>>4)
		load(2, g1)
		load(3, g1>>4)
		load(4, g1>>8)
		load(5, g2)
		load(6, g2>>4)
		load(7, g2>>8)
	}

	chain(34, 30, 0x32)
	if g0, g1, g2 := a.YTOChain(); g0 != 34 || g1 != 30 || g2 != 0x32 {
		t.Fatalf("chain = [%d %d %#x], want [34 30 0x32]", g0, g1, g2)
	}

	// A second transaction fully replaces the first (per-group reset at
	// sub-index 0/2/5) — including 12-bit values with high nibbles set.
	chain(0xC3, 0x7C3, 0xFFF)
	if g0, g1, g2 := a.YTOChain(); g0 != 0xC3 || g1 != 0x7C3 || g2 != 0xFFF {
		t.Fatalf("chain 2 = [%#x %#x %#x], want [0xC3 0x7C3 0xFFF]", g0, g1, g2)
	}
}

// TestA7IOBus_TimebaseDACAndStrobes checks the reg-5 timebase-DAC getter and
// the reg-2 settle-strobe counter (0xE2, issued by fcn.227f2 before the reg-3
// settle poll).
func TestA7IOBus_TimebaseDACAndStrobes(t *testing.T) {
	var a a7IOBus
	a.writeSelect(0x0500) // reg 5 = timebase DAC
	a.writeData(0x00AB)
	if got := a.TimebaseDAC(); got != 0xAB {
		t.Fatalf("TimebaseDAC = %#02x, want 0xAB", got)
	}

	a.writeSelect(0x6200) // reg 2, bank-3 mode as fcn.227f2 composes it
	a.writeData(0x62E2)   // settle strobe
	a.writeData(0x62E2)
	if got := a.SettleStrobes(); got != 2 {
		t.Fatalf("SettleStrobes = %d, want 2", got)
	}
	// The reg-3 gate stays settled after the strobe (conservative model).
	a.writeSelect(0x6300)
	if got := a.readData(); got&0xC0 != 0x80 {
		t.Fatalf("reg3 after strobe = %#02x & 0xC0, want 0x80", got&0xC0)
	}
}

// TestMMIO_YTOCoilDACs checks the direct YTO coil-DAC word ports (★ 2026-07-12
// A7 map): FM=0xF700, fine=0xF702, coarse=0xF704 land in the backing buffer
// and read back via the named getter.
func TestMMIO_YTOCoilDACs(t *testing.T) {
	m := NewHP8593AMMIO()
	m.Write(0x700, bus.Word, 0x0800) // B1A4 midscale
	m.Write(0x702, bus.Word, 0x0123) // B1A6 fine
	m.Write(0x704, bus.Word, 0x0ABC) // B1A8 coarse
	fm, fine, coarse := m.YTOCoilDACs()
	if fm != 0x0800 || fine != 0x0123 || coarse != 0x0ABC {
		t.Fatalf("YTOCoilDACs = %#04x/%#04x/%#04x, want 0x0800/0x0123/0x0ABC", fm, fine, coarse)
	}
}

// TestMMIO_F704Packing checks the F704 field split (reg-2 field-map RE): the
// coarse DAC is the low 12 bits and the RF attenuator control is bits [12:14].
// Writing atten=5 (0b101) + DAC=0x123 → F704 = 0x5123; RFAttenCode reads 5 and
// the DAC value is 0x123.
func TestMMIO_F704Packing(t *testing.T) {
	m := NewHP8593AMMIO()
	m.Write(0x704, bus.Word, 0x5123) // atten=5 in [12:14], coarse DAC=0x123
	if code := m.RFAttenCode(); code != 5 {
		t.Errorf("RFAttenCode = %d, want 5", code)
	}
	_, _, coarse := m.YTOCoilDACs()
	if coarse&0x0FFF != 0x123 {
		t.Errorf("coarse DAC = %#x, want 0x123", coarse&0x0FFF)
	}
}
