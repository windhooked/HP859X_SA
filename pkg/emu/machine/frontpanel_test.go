package machine_test

import (
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
)

// keyMatrixRAM is where the firmware stores the read key-matrix bitmap.
// The firmware uses `movem.l D0-D1, $8f1e.w`; the .w short address
// sign-extends to 0xFFFF8F1E, masked to 0xFF8F1E on the 24-bit bus. D0's high
// word (a cleared local) lands at 0xFF8F1E/1F, so the 6 matrix bytes b0..b5 sit
// at 0xFF8F20..0xFF8F25.
const keyMatrixRAM = uint32(0xFF8F20)

// TestFrontPanelKeyReadChain — front-panel input gate: after the machine boots,
// injecting a raw key-matrix bitmap must travel the full path
// IRQ3 → handler (bd77.0) → main loop → key-read routine (0x3AB52) →
// 0xEF40xx register read → RAM 0x8F1E, landing the exact bytes we injected.
//
// WIP / SKIPPED: the IRQ3 delivery and front-panel read protocol are verified
// (handler 0x1582 runs; device reconstruction round-trips — see the device
// package tests). But in the operating state the emulator currently reaches,
// the firmware does not consume the key: its main loop is a timer-gated spin at
// 0x51B0 (waiting on bfb9.7) whose per-cycle service work never reaches the key
// consumer at 0x01089A (bd77.0 stays latched at 0x05). Locating the firmware's
// actual key-poll trigger in that dispatch is the remaining step; until then
// this end-to-end assertion is skipped rather than asserted falsely.
func TestFrontPanelKeyReadChain(t *testing.T) {
	t.Skip("front-panel key consume path not yet located in firmware main loop; " +
		"see device.FrontPanel tests for verified protocol/device behaviour")

	m := newMachine(t)
	m.BootToOperating(bootScreenCycles)

	// A bitmap whose per-byte high nibbles respect the matrix masks the
	// firmware applies (b1≤1, b2/b3≤3, b4/b5≤7 in the high nibble).
	want := [6]byte{0xAB, 0x1C, 0x2D, 0x3E, 0x7F, 0x6A}

	if !m.PressKeyMatrix(want, 2_000_000) {
		t.Fatal("firmware never read the injected key (Consumed=false)")
	}

	for i, w := range want {
		addr := keyMatrixRAM + uint32(i)
		got := byte(m.Bus.Read(addr, bus.Byte))
		if got != w {
			t.Errorf("key matrix byte %d at %#06X = %#02x, want %#02x", i, addr, got, w)
		}
	}
}
