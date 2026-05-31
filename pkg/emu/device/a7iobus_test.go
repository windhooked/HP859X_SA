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

	// Select reg 3 (mode nibble 0x1 in the top, as the firmware composes it),
	// write a value, read it back.
	a.writeSelect(0x1300)
	a.writeData(0xBEEF)
	if got := a.readData(); got != 0xBEEF {
		t.Fatalf("reg3 readback = %#04x, want 0xBEEF", got)
	}

	// A different register is independent and reads back 0 until written.
	a.writeSelect(0x0500) // reg 5
	if got := a.readData(); got != 0 {
		t.Fatalf("unwritten reg5 = %#04x, want 0", got)
	}
	a.writeData(0x1234)

	// Re-selecting reg 3 still returns its own value (no cross-talk).
	a.writeSelect(0x9300) // reg 3, different mode nibble
	if got := a.readData(); got != 0xBEEF {
		t.Fatalf("reg3 after touching reg5 = %#04x, want 0xBEEF", got)
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
