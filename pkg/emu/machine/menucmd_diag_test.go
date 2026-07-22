package machine

import (
	"image/png"
	"os"
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/device"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

// TestMenuCmdDiag — the documented remote command `MENU n;` ("Selects and
// displays the softkey menus", HP 8590 Programmer's Guide) typed through the
// PROVEN AT path. If the menu system comes alive (b071.0 set, menu installed,
// labels drawn), the whole menu machinery works and the residual was only that
// nothing ever invoked it (the AT F-keys map to MKR/SPAN/AMPL functions, not to
// the FREQUENCY/SPAN hardkeys 'X'/'Y' that turn labels on). DIAG=1.
func TestMenuCmdDiag(t *testing.T) {
	if os.Getenv("DIAG") == "" {
		t.Skip("DIAG=1")
	}
	rom, err := romloader.LoadDir("../../../hp8593a_eeproms")
	if err != nil {
		t.Skip("rom")
	}
	m, _ := New8593A(rom)
	m.CPU.Reset()
	m.MMIO.SweepActive = true
	m.SweepDrive = true
	m.BootToOperatingWithSweep(250_000_000)
	chip := m.MMIO.Display.Chip

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
		cmd = "MENU 1;"
	}
	rd := func(a uint32, sz bus.Size) uint32 { return m.Bus.Read(a, sz) }
	t.Logf("BEFORE: b070/71=%#06x menu(956a)=%d vtable(9566)=%#x",
		rd(0xFFB070, bus.Word), rd(0xFF956A, bus.Word), rd(0xFF9566, bus.Long))
	typeKey(device.ATKeyF8)
	for _, r := range cmd {
		if k, ok := atKeyFor(r); ok {
			typeKey(k)
		}
	}
	typeKey(device.ATKeyEnter)
	m.driveHPIB(400_000_000, nil)
	t.Logf("AFTER %q: b070/71=%#06x menu(956a)=%d vtable(9566)=%#x",
		cmd, rd(0xFFB070, bus.Word), rd(0xFF956A, bus.Word), rd(0xFF9566, bus.Long))
	chip.SetRenderLive(true)
	if f, err := os.Create("../../../screens/menucmd.png"); err == nil {
		png.Encode(f, chip.RenderScanout())
		f.Close()
		t.Log("rendered screens/menucmd.png")
	}
}
