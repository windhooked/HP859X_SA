package machine

import (
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

// TestShowMenuDiag documents (2026-07-16) that the menu-INSTALL mechanism works
// when triggered directly: SHOW_MENU (fcn.5ada4, trampoline 0xc40) → fcn.5a918
// changes the active-menu state (0x956a index; 0x9566 vtable = 0xFF9594+idx*0xE0
// for idx≥4). So the install mechanism is NOT the gap. The gap is (a) the
// event→SHOW_MENU binding — a menu key (KEYEXC, a direct-C command) resolves to a
// class-0 word that dead-ends in the data-entry path (fcn.12b10 @0x12d80), never
// reaching SHOW_MENU; and (b) the softkey-label REDRAW (fcn.e7a2, gated on
// b071.0) does not fire, so even after a direct SHOW_MENU the on-screen labels
// don't repaint. Both are the same direct-C-dispatch residual as CONTS. Do NOT
// force these (half-mock, per the EnterContinuousSpectrum lesson) — the faithful
// fix is the direct-C command dispatch. See docs/MEASURE_MODE_HANDOFF.md.
func TestShowMenuDiag(t *testing.T) {
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

	menuBefore := m.Bus.Read(0xFF956A, bus.Word)
	const sentinel = 0x000FFFFC
	sp := m.CPU.Reg(cpu.A7) - 64
	m.Bus.Write(sp-4, bus.Long, sentinel)
	m.CPU.SetReg(cpu.A7, sp-4)
	m.CPU.SetReg(cpu.D0, 5) // a menu index that changes the vtable
	m.CPU.SetReg(cpu.PC, 0x00000c40)
	ret := false
	for i := 0; i < 600; i++ {
		if _, h := m.CPU.RunUntil(20000, sentinel); h {
			ret = true
			break
		}
		m.CPU.SetIRQ(5)
		m.CPU.Run(400)
		m.CPU.SetIRQ(0)
	}
	menuAfter := m.Bus.Read(0xFF956A, bus.Word)
	vtable := m.Bus.Read(0xFF9566, bus.Long)
	t.Logf("SHOW_MENU(5) ret=%v: active-menu 0x956a %d→%d, vtable 0x9566=%#x",
		ret, menuBefore, menuAfter, vtable)
	if menuAfter == menuBefore {
		t.Error("SHOW_MENU did not change the active-menu state — install mechanism broken")
	}
}
