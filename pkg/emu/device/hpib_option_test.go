package device

import (
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
)

// TestInstallHPIB verifies the HP-IB option-board model: InstallHPIB sets the
// I/O-board descriptor byte (0xFFF11E=4 → bf09=4=HP-IB) and the option-board
// serial-chip status register (0xFFF148) reads "ready, not busy" (0x01) so the
// boot init handshake completes. Without the option, the descriptor and status
// read 0 (no option present).
func TestInstallHPIB(t *testing.T) {
	m := NewHP8593AMMIO()

	// Default: option absent → descriptor byte 0, status 0.
	if v := m.Read(0x11E, bus.Byte); v != 0 {
		t.Errorf("default 0xF11E = %#02x, want 0 (no option)", v)
	}
	if v := m.Read(0x148, bus.Byte); v != 0 {
		t.Errorf("default 0xF148 = %#02x, want 0 (no option)", v)
	}

	m.InstallHPIB()
	if !m.HPIBInstalled {
		t.Fatal("InstallHPIB did not set HPIBInstalled")
	}
	// Descriptor byte 0xF11E = 4 (HP-IB), which the firmware reads into bf09.
	if v := m.Read(0x11E, bus.Byte); v != 0x04 {
		t.Errorf("0xF11E after InstallHPIB = %#02x, want 0x04 (HP-IB)", v)
	}
	// Status 0xF148 = 0x01: bit 0 (ack) set so "wait-for-set" polls pass, bit 1
	// (busy) clear so "wait-for-clear" polls pass. Both poll types in the boot
	// init handshake (ROM 0x300A set-poll, 0x3070 clear-poll) then complete.
	if v := m.Read(0x148, bus.Byte); v != 0x01 {
		t.Errorf("0xF148 after InstallHPIB = %#02x, want 0x01 (ready, not busy)", v)
	}
}
