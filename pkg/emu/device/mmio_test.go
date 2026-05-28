package device

import (
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
)

// TestSCIStatusReady verifies that the SCI status byte (offset 0x5FD) always
// reads with bits 1 and 2 set, both before and after command writes.
func TestSCIStatusReady(t *testing.T) {
	m := NewHP8593AMMIO()

	const want = sciStatusReady
	// Fresh device: status must already be ready (bits 0, 1, 2).
	got := m.Read(sciStatusOffset, bus.Byte)
	if got&want != want {
		t.Errorf("initial SCI status = %#02x, want bits 0+1+2 set (%#02x)", got, want)
	}

	// Write a command to 0x5FC (clears status in naive RAM, but we re-assert).
	m.Write(0x5FC, bus.Word, 0x0002)
	got = m.Read(sciStatusOffset, bus.Byte)
	if got&want != want {
		t.Errorf("after command write, SCI status = %#02x, want bits 0+1+2 still set", got)
	}

	// Explicit clear of status byte: override must still win on next read.
	m.b[sciStatusOffset] = 0x00 // simulate cleared-without-command write path
	got = m.Read(sciStatusOffset, bus.Byte)
	if got&want != want {
		t.Errorf("after manual clear, SCI status read = %#02x, want bits 0+1+2 still set", got)
	}
}

// TestSCIStatusWordRead verifies that a word read at 0x5FC also exposes the
// ready bits in the low byte (= 0x5FD position).
func TestSCIStatusWordRead(t *testing.T) {
	m := NewHP8593AMMIO()
	w := m.Read(0x5FC, bus.Word)
	if w&sciStatusReady != sciStatusReady {
		t.Errorf("word read at 0x5FC = %#04x, want low byte bits 0+1+2 set", w)
	}
}

// TestSweepStatusReady verifies that word reads of the sweep-status register
// (offset 0x300) always have bit 12 set, even after the firmware writes a
// value that clears it. This unblocks the sweep-ready polling loop at 0xF608.
func TestSweepStatusReady(t *testing.T) {
	m := NewHP8593AMMIO()

	// Fresh device: bit 12 must already be set.
	got := m.Read(sweepStatusOffset, bus.Word)
	if got&sweepStatusReady != sweepStatusReady {
		t.Errorf("initial sweep status = %#04x, want bit 12 (0x%04x) set", got, sweepStatusReady)
	}

	// Firmware writes a sweep-step value that does NOT include bit 12 (e.g. 0x0004).
	m.Write(sweepStatusOffset, bus.Word, 0x0004)
	got = m.Read(sweepStatusOffset, bus.Word)
	if got&sweepStatusReady != sweepStatusReady {
		t.Errorf("after write 0x0004, sweep status = %#04x, want bit 12 still set", got)
	}
	// But written bits are preserved too (bit 2 should remain).
	if got&0x0004 != 0x0004 {
		t.Errorf("after write 0x0004, bit 2 lost: got %#04x", got)
	}

	// Manual clear of the backing byte: override must still win.
	m.b[sweepStatusOffset] = 0x00
	m.b[sweepStatusOffset+1] = 0x00
	got = m.Read(sweepStatusOffset, bus.Word)
	if got&sweepStatusReady != sweepStatusReady {
		t.Errorf("after manual clear, sweep status = %#04x, want bit 12 still set", got)
	}
}

// TestMMIOReadWriteRoundtrip checks that ordinary registers (not the SCI status
// override) can be written and read back correctly.
func TestMMIOReadWriteRoundtrip(t *testing.T) {
	m := NewHP8593AMMIO()

	// 82C55A control register at offset 0x007 (byte write).
	m.Write(0x007, bus.Byte, 0x82)
	if got := m.Read(0x007, bus.Byte); got != 0x82 {
		t.Errorf("PPI control = %#02x, want 0x82", got)
	}

	// TMS9914A HP-IB controller at offset 0x600..0x60F. Reads and writes
	// at the same chip-local address access DIFFERENT registers (read =
	// status / data, write = mask / data / auxiliary command), so a
	// write-then-read of 0xFF at 0x606 (= BSR read / ADR write on the
	// chip) returns the read-side register's value (BSR), NOT what we
	// wrote to ADR. The minimal model in tms9914a.go initialises both
	// to zero, so we expect 0 back.
	m.Write(0x606, bus.Byte, 0xFF)
	if got := m.Read(0x606, bus.Byte); got != 0x00 {
		t.Errorf("HP-IB reg 0x606 read after write = %#02x, want 0x00 (read-side BSR, not write-side ADR)",
			got)
	}
}
