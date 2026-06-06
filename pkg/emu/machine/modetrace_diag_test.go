package machine

import (
	"os"
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
)

// TestModeTraceDiag — STEP 1 of the rigorous b0ec mode-decision trace. Watchpoint
// 0xFFB0EC; log every write (PC + value + order) over a long natural boot. The
// firmware boots to config mode (0x1A) by design and should transition to spectrum
// MEASURE (0x31). This shows the exact write sequence + which write leaves it at the
// final value, so step 2 can walk back to the gating read. DIAG=1.
func TestModeTraceDiag(t *testing.T) {
	if os.Getenv("DIAG") == "" {
		t.Skip("DIAG=1")
	}
	m := diagBootMachine(t)
	type w struct {
		seq     int
		pc, val uint32
	}
	var writes []w
	seq := 0
	m.Bus.OnWrite = func(addr uint32, sz bus.Size, val uint32) {
		if addr == 0xFFB0EC && sz == bus.Word {
			seq++
			writes = append(writes, w{seq, m.CPU.Reg(cpu.PC), val & 0xFFFF})
		}
	}
	m.BootToOperatingWithSweep(260_000_000)
	m.Bus.OnWrite = nil

	t.Logf("total b0ec writes during boot: %d", len(writes))
	// Print first 8 and last 24 (the steady-state churn matters most).
	show := func(label string, ws []w) {
		for _, e := range ws {
			t.Logf("  %s #%-4d PC=0x%05X  b0ec <- 0x%04X", label, e.seq, e.pc, e.val)
		}
	}
	if len(writes) <= 32 {
		show("", writes)
	} else {
		show("first", writes[:8])
		t.Logf("  ... (%d more) ...", len(writes)-32)
		show("last", writes[len(writes)-24:])
	}
	t.Logf("FINAL b0ec=0x%04X (config=0x01/0x1A, spectrum-measure=0x2D/0x31/0x36)", m.Bus.Read(0xFFB0EC, bus.Word))
}
