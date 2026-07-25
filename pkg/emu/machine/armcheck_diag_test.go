package machine

import (
	"os"
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

// Scratch: what buffer end (bf30) does the firmware arm, and where does A5
// actually wrap per sweep?
func TestArmCheckDiag(t *testing.T) {
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
	maxA5 := uint32(0)
	wraps := 0
	last := uint32(0)
	// Sample (idx, b07a bit7, b07a) transitions across sweeps.
	lastB7 := -1
	for i := 0; i < 800; i++ {
		m.bootLoop(100_000, nil)
		a5 := m.CPU.Reg(cpu.A5)
		b07a := int(m.Bus.Read(0xFFB07A, bus.Word))
		b7 := (b07a >> 7) & 1
		idx := -1
		if a5 >= 0x2FD508 && a5 < 0x2FE000 {
			idx = int(a5-0x2FD508) / 2
			if a5 < last && last > 0x2FD508 {
				wraps++
			}
			if a5 > maxA5 {
				maxA5 = a5
			}
			last = a5
		}
		if b7 != lastB7 {
			t.Logf("t%3d: b07a=%#06x bit7=%d at idx=%d", i, b07a, b7, idx)
			lastB7 = b7
		}
	}
	t.Logf("max A5 = %#x (idx %d); wraps=%d; bf30=%#x (idx %d)",
		maxA5, (maxA5-0x2FD508)/2, wraps, m.Bus.Read(0xFFBF30, bus.Long), (m.Bus.Read(0xFFBF30, bus.Long)-0x2FD508)/2)
	t.Logf("BF3C (samples/slot) = %d  BF3E (countdown) = %d  A9A2 = %d  A9A4 = %d",
		m.Bus.Read(0xFFBF3C, bus.Word), m.Bus.Read(0xFFBF3E, bus.Word),
		m.Bus.Read(0xFFA9A2, bus.Word), m.Bus.Read(0xFFA9A4, bus.Word))
	t.Logf("marker cells: B0E2=%d B0E6=%d B073=%#x BA64=%d BA68=%d",
		m.Bus.Read(0xFFB0E2, bus.Word), m.Bus.Read(0xFFB0E6, bus.Word),
		m.Bus.Read(0xFFB073, bus.Byte), m.Bus.Read(0xFFBA64, bus.Word), m.Bus.Read(0xFFBA68, bus.Word))
}
