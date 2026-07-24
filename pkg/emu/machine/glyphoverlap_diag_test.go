package machine

import (
	"os"
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/device"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

// Scratch: during the F9 menu-switch label redraw, are label-region clears
// executed or skipped (SCLR no-area-def no-op branch)?
func TestGlyphOverlapDiag(t *testing.T) {
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
	typeKey := func(k device.ATKey) {
		m.ATKeyboard.Enqueue(device.ATMake(k)...)
		m.driveHPIB(4_000_000, func() bool { return !m.ATKeyboard.Pending() })
		m.ATKeyboard.Enqueue(device.ATBreak(k)...)
		m.driveHPIB(4_000_000, func() bool { return !m.ATKeyboard.Pending() })
	}
	b0 := []int{chip.ScreenClears, chip.AreaClears, chip.SCLRNoAreaDef}
	typeKey(device.ATKeyF9) // FREQUENCY: menu switch + label redraw
	m.driveHPIB(100_000_000, nil)
	typeKey(device.ATKeyF2) // START FREQ: labels paint (b070.0 set)
	m.driveHPIB(150_000_000, nil)
	t.Logf("deltas over F9+F2: ScreenClears=%d AreaClears=%d SCLRNoAreaDef=%d (last skipped: rwp=%#x ax=%d ay=%d pat=%#x)",
		chip.ScreenClears-b0[0], chip.AreaClears-b0[1], chip.SCLRNoAreaDef-b0[2],
		chip.SCLRNoAreaLast[0], chip.SCLRNoAreaLast[1], chip.SCLRNoAreaLast[2], chip.SCLRNoAreaLast[3])
}
