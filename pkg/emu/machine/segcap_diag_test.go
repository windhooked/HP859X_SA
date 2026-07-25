package machine

import (
	"fmt"
	"os"
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

// Scratch: capture the steady-state FIFO stream for segment-choreography
// analysis (erase-vs-APLL x coverage).
func TestSegCapDiag(t *testing.T) {
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
	for i := 0; i < 40; i++ {
		m.bootLoop(2_000_000, nil)
	}
	m.MMIO.Display.Chip.StartCmdCapture(262144)
	for i := 0; i < 20; i++ {
		m.bootLoop(2_000_000, nil)
	}
	ws := m.MMIO.Display.Chip.CmdCapture()
	f, _ := os.Create("/tmp/segcap.txt")
	for _, w := range ws {
		fmt.Fprintf(f, "%04X\n", w)
	}
	f.Close()
	t.Logf("captured %d words -> /tmp/segcap.txt", len(ws))
}
