package machine

import (
	"image/png"
	"os"
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/device"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

// Scratch: navigate several menus + softkeys in ONE machine (the scenario that
// previously hit the unimplemented WT 0x4800 panic via the softkey-5 redraw).
func TestMenuNavSmokeDiag(t *testing.T) {
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
	typeKey := func(k device.ATKey) {
		m.ATKeyboard.Enqueue(device.ATMake(k)...)
		m.driveHPIB(4_000_000, func() bool { return !m.ATKeyboard.Pending() })
		m.ATKeyboard.Enqueue(device.ATBreak(k)...)
		m.driveHPIB(4_000_000, func() bool { return !m.ATKeyboard.Pending() })
	}
	seq := []device.ATKey{
		device.ATKeyF5,  // softkey 5 on boot config menu — the WT trigger
		device.ATKeyF9,  // FREQUENCY
		device.ATKeyF2,  // START FREQ
		device.ATKeyF10, // SPAN
		device.ATKeyF1, device.ATKeyF3,
		device.ATKeyF11, // AMPLITUDE
		device.ATKeyF4,
		device.ATKeyF9,
	}
	for i, k := range seq {
		typeKey(k)
		m.driveHPIB(60_000_000, nil)
		t.Logf("step %d key=%v: b1ee=%d b1e4=%#x b070=%#x", i, k,
			m.Bus.Read(0xFFB1EE, bus.Word), m.Bus.Read(0xFFB1E4, bus.Word), m.Bus.Read(0xFFB070, bus.Word))
	}
	m.MMIO.Display.Chip.SetRenderLive(true)
	if f, err := os.Create("../../../screens/menu_nav.png"); err == nil {
		png.Encode(f, m.MMIO.Display.Chip.RenderScanout())
		f.Close()
		t.Log("rendered screens/menu_nav.png")
	}
}
