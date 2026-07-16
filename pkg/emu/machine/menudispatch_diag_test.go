package machine

import (
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/device"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

// TestMenuDispatchDiag documents the softkey/menu-ACTION completion gap
// (2026-07-16): a menu key (F9=MKR) REACHES the firmware and sets a key-specific
// command word in b1e4 (F9→0x07, F10→0x08, F11→0x05), but the menu does NOT
// switch. Root: 0x07 is a CLASS-0 command word (class byte = b1e4>>8 = 0), so
// the record processor fcn.12b10 routes it to the class-0 data-entry path
// (0x12dd6), not the class dispatcher fcn.12288 / the menu-install fcn.5a918.
// So the event→menu-ACTION binding is missing — the same class as the CONTS
// softkey-action residual (docs/MEASURE_MODE_HANDOFF.md). Reception works; the
// action does not complete. This is the interactivity residual.
func TestMenuDispatchDiag(t *testing.T) {
	if testing.Short() {
		t.Skip("boot")
	}
	rom, err := romloader.LoadDir("../../../hp8593a_eeproms")
	if err != nil {
		t.Skip("rom")
	}
	m, _ := New8593A(rom)
	m.CPU.Reset()
	m.MMIO.SweepActive = true
	m.SweepDrive = true
	m.BootToOperatingWithSweep(200_000_000)
	rd := func(a uint32) uint32 { return m.Bus.Read(a, bus.Word) }

	menuBefore := rd(0xFF956A)
	install := 0
	m.ATKeyboard.Enqueue(device.ATMake(device.ATKeyF9)...)
	m.ATKeyboard.Enqueue(device.ATBreak(device.ATKeyF9)...)
	for j := 0; j < 20000 && install == 0; j++ {
		if m.ATKeyboard.Pending() {
			m.CPU.SetIRQ(4)
			m.CPU.Run(600)
			m.CPU.SetIRQ(0)
		}
		if _, h := m.CPU.RunUntil(2000, 0x0005a918); h { // fcn.5a918 = menu install
			install++
		}
		if j%5 == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Run(400)
			m.CPU.SetIRQ(0)
		}
		m.DriveOneSweepChunk()
	}
	b1e4 := rd(0xFFB1E4)
	class := (b1e4 >> 8) & 0xFF
	t.Logf("F9 (MKR menu): b1e4=%#x (class byte=%#x) menu(956a) %d→%d  menu-install(fcn.5a918)=%d",
		b1e4, class, menuBefore, rd(0xFF956A), install)

	// Document the gap as invariants (this is the KNOWN residual, not a failure):
	if b1e4 == 0 {
		t.Error("F9 produced no command word — key not received")
	}
	if class != 0 {
		t.Logf("NOTE: b1e4 is now class-bearing (%#x) — the menu-action binding may have changed; re-verify", class)
	}
	if install > 0 || rd(0xFF956A) != menuBefore {
		t.Logf("★ menu-install fired / menu changed — the softkey-action gap may be CLOSED; promote to a positive assertion")
	}
}
