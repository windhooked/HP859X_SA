package machine

import (
	"os"
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

// Scratch: during a natural run, which sample path is active (polled vs IRQ6)
// and does A5 track the buffer position at poll time?
func TestPollSyncDiag(t *testing.T) {
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
	// Sample A5 + polledReads periodically over a run.
	type snap struct {
		polled int
		a5     uint32
		pos    int
	}
	var snaps []snap
	for i := 0; i < 120; i++ {
		m.bootLoop(500_000, nil)
		snaps = append(snaps, snap{m.PolledReads(), m.CPU.Reg(cpu.A5), m.MMIO.Sweep.Pos()})
	}
	last := snap{-1, 0, -1}
	shown := 0
	for i, s := range snaps {
		if s.polled != last.polled || s.pos != last.pos {
			if shown < 25 {
				t.Logf("t%3d: polledReads=%6d  enginePos=%3d  A5=%#x", i, s.polled, s.pos, s.a5)
			}
			shown++
			last = s
		}
	}
	t.Logf("total changes: %d; final polled=%d enginePos=%d", shown, snaps[len(snaps)-1].polled, snaps[len(snaps)-1].pos)
}
