package machine

import (
	"image/png"
	"os"
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/device"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

// TestFrontPanelMenuKeys locks the end-to-end front-panel menu interactivity
// (2026-07-22): the faithful AT key path drives the firmware's own menu system.
//
//	F9 (FREQUENCY) -> key FIFO 0xBB58 code 0x2D -> key# 8045 -> ROM record
//	  `IF(MSBIT(8,0));FA;ELSE CF;ENDIF;MN25;` -> CF (letter 'V' -> b1e4=7,
//	  CENTER FREQ active) + MN25 (b1ee=25: FREQUENCY menu current)
//	F2 (softkey 2) -> code 0x22 -> softkey record 4146 `KH'START|FREQ',ACTVF FA;`
//	  -> letter 'X' -> b1e4=9 (START FREQ active) + fcn.119f8(1) -> b070/b071
//	  bit0 SET (softkey labels shown; the FREQUENCY menu paints, annotation
//	  switches to START/STOP)
//
// b1ee (0xFFB1EE) is the hardkey-menu register (boot menu = 30, the config
// menu); 0x956A/0x9566 belong to the separate DLP user-menu machinery. Full
// pipeline: docs/KEYBOARD_MAP.md. Renders screens/freq_menu.png.
func TestFrontPanelMenuKeys(t *testing.T) {
	if testing.Short() {
		t.Skip("boot test")
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
	chip := m.MMIO.Display.Chip

	typeKey := func(k device.ATKey) {
		m.ATKeyboard.Enqueue(device.ATMake(k)...)
		m.driveHPIB(4_000_000, func() bool { return !m.ATKeyboard.Pending() })
		m.ATKeyboard.Enqueue(device.ATBreak(k)...)
		m.driveHPIB(4_000_000, func() bool { return !m.ATKeyboard.Pending() })
	}
	rd := func(a uint32, sz bus.Size) uint32 { return m.Bus.Read(a, sz) }

	if got := rd(0xFFB1EE, bus.Word); got != 30 {
		t.Errorf("boot menu b1ee = %d, want 30 (config menu)", got)
	}

	typeKey(device.ATKeyF9) // FREQUENCY
	m.driveHPIB(100_000_000, nil)
	if got := rd(0xFFB1EE, bus.Word); got != 25 {
		t.Errorf("after F9: menu b1ee = %d, want 25 (FREQUENCY menu)", got)
	}
	if got := rd(0xFFB1E4, bus.Word); got != 7 {
		t.Errorf("after F9: b1e4 = %#x, want 7 (CENTER FREQ active)", got)
	}

	typeKey(device.ATKeyF2) // softkey 2 = START FREQ
	m.driveHPIB(200_000_000, nil)
	if got := rd(0xFFB1E4, bus.Word); got != 9 {
		t.Errorf("after F2: b1e4 = %#x, want 9 (START FREQ active)", got)
	}
	if got := rd(0xFFB070, bus.Word); got&1 != 1 {
		t.Errorf("after F2: b070 = %#x, want bit0 set (softkey labels shown)", got)
	}

	chip.SetRenderLive(true)
	if f, err := os.Create("../../../screens/freq_menu.png"); err == nil {
		png.Encode(f, chip.RenderScanout())
		f.Close()
	}
}
