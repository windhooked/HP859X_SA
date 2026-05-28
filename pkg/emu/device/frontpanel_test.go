package device

import (
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
)

// reconstructMatrix applies the firmware's key-read packing (ROM 0x3AB52) to the
// device's nibble registers, yielding the 6-byte key-matrix bitmap the firmware
// would store to RAM 0x8F1E.
func reconstructMatrix(f *FrontPanel) [6]byte {
	rd := func(off uint32) byte { return byte(f.Read(off, bus.Byte)) }
	return [6]byte{
		(rd(0x17)&0x0F)<<4 | (rd(0x15) & 0x0F),
		(rd(0x13)&0x01)<<4 | (rd(0x11) & 0x0F),
		(rd(0x0F)&0x03)<<4 | (rd(0x0D) & 0x0F),
		(rd(0x0B)&0x03)<<4 | (rd(0x09) & 0x0F),
		(rd(0x07)&0x07)<<4 | (rd(0x05) & 0x0F),
		(rd(0x03)&0x07)<<4 | (rd(0x01) & 0x0F),
	}
}

// TestFrontPanelMatrixRoundTrip verifies that InjectMatrix populates the nibble
// registers such that the firmware's reconstruction formula recovers exactly
// the injected bitmap (for values that respect the per-byte high-nibble masks).
func TestFrontPanelMatrixRoundTrip(t *testing.T) {
	f := NewFrontPanel()
	// High nibbles respect masks: b1≤1, b2/b3≤3, b4/b5≤7.
	want := [6]byte{0xAB, 0x1C, 0x2D, 0x3E, 0x7F, 0x6A}
	f.InjectMatrix(want)

	// Reading register 0x17 commits the read (Consumed); reconstruct first
	// reads it, so check Pending before and Consumed after.
	if !f.Pending() {
		t.Fatal("InjectMatrix did not arm Pending()")
	}
	got := reconstructMatrix(f)
	if got != want {
		t.Errorf("reconstructed matrix = %v, want %v", got, want)
	}
	if !f.Consumed() {
		t.Error("reading register 0x17 should mark the event Consumed")
	}
}

// TestFrontPanelHandshakeReady verifies the status register (0x1B) always reads
// with the busy bit (bit 1) clear, so the firmware's read handshake completes.
func TestFrontPanelHandshakeReady(t *testing.T) {
	f := NewFrontPanel()
	// Firmware writes 0x4 then 0x5 to the status reg during the handshake.
	f.Write(fpStatusReg, bus.Byte, 0x04)
	if v := f.Read(fpStatusReg, bus.Byte); v&0x02 != 0 {
		t.Errorf("status after write 0x4 = %#02x, busy bit set", v)
	}
	f.Write(fpStatusReg, bus.Byte, 0x05)
	if v := f.Read(fpStatusReg, bus.Byte); v&0x02 != 0 {
		t.Errorf("status after write 0x5 = %#02x, busy bit set", v)
	}
}

// TestFrontPanelReleaseClears verifies Release() clears the matrix and flags.
func TestFrontPanelReleaseClears(t *testing.T) {
	f := NewFrontPanel()
	f.SetBit(0, 3)
	if !f.Pending() {
		t.Fatal("SetBit did not arm Pending()")
	}
	f.Release()
	if f.Pending() {
		t.Error("Release() left Pending() set")
	}
	if got := reconstructMatrix(f); got != ([6]byte{}) {
		t.Errorf("Release() left matrix %v, want all zero", got)
	}
}
