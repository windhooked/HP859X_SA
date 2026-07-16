package machine

import (
	"os"
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
	"github.com/windhooked/HP859X_SA/pkg/emu/device"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

// TestKeyExcTypedDiag asks the actionable fix question: does the command a menu
// key emits — "KEYEXC <keycode>;" — actually switch the menu when delivered
// through the WORKING typed-command parser (the same path that runs CAL DISP)?
//
// F9 (MKR) emits KEYEXC 30003 (0x7533). If typing "KEYEXC 30003;" reaches the
// menu-install machinery (SHOW_MENU 0xc40 / fcn.5a918@0x5a918 / active menu
// 0x956a changes), then front-panel menu interactivity is reachable through the
// parser and the fix is to route key events there. If it dead-ends the same way
// the front-panel event does (b1e4 class-0, no install), the residual is the
// KEYEXC handler itself, not the delivery path. Diagnostic (DIAG=1, long drive).
func TestKeyExcTypedDiag(t *testing.T) {
	if os.Getenv("DIAG") == "" {
		t.Skip("set DIAG=1 to run the KEYEXC-typed menu-switch probe")
	}
	rom, err := romloader.LoadDir("../../../hp8593a_eeproms")
	if err != nil {
		t.Skip("rom not available")
	}
	m, _ := New8593A(rom)
	m.CPU.Reset()
	m.MMIO.SweepActive = true
	m.SweepDrive = true
	m.BootToOperatingWithSweep(250_000_000)

	const (
		pcParser   = 0x00058c2e // command parser
		pcResolve  = 0x000320fe // name resolve
		pcShowMenu = 0x00000c40 // SHOW_MENU trampoline
		pcInstall  = 0x0005a918 // fcn.5a918 active-menu install
	)
	hits := map[uint32]int{}
	watchOn := false
	m.Bus.OnRead = func(addr uint32, sz bus.Size, val uint32) {
		if !watchOn {
			return
		}
		pc := m.CPU.Reg(cpu.PC)
		switch {
		case pc >= pcParser && pc <= 0x58ebc:
			hits[pcParser]++
		case pc >= pcResolve && pc <= 0x32300:
			hits[pcResolve]++
		case pc >= pcShowMenu && pc <= pcShowMenu+4:
			hits[pcShowMenu]++
		case pc >= pcInstall && pc <= pcInstall+0x30:
			hits[pcInstall]++
		}
	}

	menuBefore := m.Bus.Read(0xFF956A, bus.Word)
	vtBefore := m.Bus.Read(0xFF9566, bus.Long)
	b1e4Before := m.Bus.Read(0xFFB1E4, bus.Word)

	atKeyFor := func(r rune) (device.ATKey, bool) {
		switch {
		case r >= 'A' && r <= 'Z':
			return device.ATKeyA + device.ATKey(r-'A'), true
		case r >= '0' && r <= '9':
			return device.ATKey0 + device.ATKey(r-'0'), true
		case r == ' ':
			return device.ATKeySpace, true
		case r == ';':
			return device.ATKeySemicolon, true
		}
		return 0, false
	}
	typeKey := func(k device.ATKey) {
		m.ATKeyboard.Enqueue(device.ATMake(k)...)
		m.driveHPIB(4_000_000, func() bool { return !m.ATKeyboard.Pending() })
		m.ATKeyboard.Enqueue(device.ATBreak(k)...)
		m.driveHPIB(4_000_000, func() bool { return !m.ATKeyboard.Pending() })
	}

	cmd := os.Getenv("ATCMD")
	if cmd == "" {
		cmd = "KEYEXC 30003;"
	}
	watchOn = true
	typeKey(device.ATKeyF8)
	for _, r := range cmd {
		if k, ok := atKeyFor(r); ok {
			typeKey(k)
		}
	}
	typeKey(device.ATKeyEnter)
	m.driveHPIB(800_000_000, func() bool {
		return hits[pcInstall] > 0 || m.Bus.Read(0xFF956A, bus.Word) != menuBefore
	})
	watchOn = false

	menuAfter := m.Bus.Read(0xFF956A, bus.Word)
	vtAfter := m.Bus.Read(0xFF9566, bus.Long)
	b1e4After := m.Bus.Read(0xFFB1E4, bus.Word)
	t.Logf("=== typed %q ===", cmd)
	t.Logf("parser=%d resolve=%d SHOW_MENU(0xc40)=%d install(fcn.5a918)=%d",
		hits[pcParser], hits[pcResolve], hits[pcShowMenu], hits[pcInstall])
	t.Logf("active-menu 0x956a: %d -> %d ; vtable 0x9566: %#x -> %#x ; b1e4: %#x -> %#x",
		menuBefore, menuAfter, vtBefore, vtAfter, b1e4Before, b1e4After)
	switch {
	case hits[pcInstall] > 0 || menuAfter != menuBefore:
		t.Logf("VERDICT: typed KEYEXC REACHED the menu-install machinery — front-panel menu interactivity is reachable via the parser")
	case hits[pcResolve] > 0:
		t.Logf("VERDICT: KEYEXC parsed+resolved but never installed a menu — the KEYEXC handler is the residual, not the delivery path")
	case hits[pcParser] > 0:
		t.Logf("VERDICT: reached the parser but KEYEXC not resolved — name-lookup gap (do not read as dispatch-blocked)")
	default:
		t.Logf("VERDICT: command never reached the parser — harness gate, inconclusive")
	}
}
