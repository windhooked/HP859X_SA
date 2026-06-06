package machine

import (
	"os"
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
)

// TestKeyMapProbeDiag — empirical front-panel key probe. The dispatch fcn.67c is
// gated on bc67.1 + b072.14, which the Rev L firmware NEVER sets (they come from
// the front-panel µC as a bus master). PressKey forces those gates. This sweeps
// the 48 matrix bits via PressKey and checks (a) does the dispatch fcn.67c run
// once the gates are forced, and (b) does any bit produce a softkey/menu action
// (0x1EFDE) or differentiate. DIAG=1.
func TestKeyMapProbeDiag(t *testing.T) {
	if os.Getenv("DIAG") == "" {
		t.Skip("DIAG=1")
	}
	m := diagBootMachine(t)
	m.BootToOperatingWithSweep(250_000_000)

	dispatch, softkey := 0, 0
	m.Bus.OnRead = func(addr uint32, sz bus.Size, val uint32) {
		switch pc := m.CPU.Reg(cpu.PC); {
		case pc >= 0x5a0e8 && pc <= 0x5a126:
			dispatch++
		case pc >= 0x1efde && pc <= 0x1f100:
			softkey++
		}
	}

	results := 0
	for b := 0; b < 6; b++ {
		for bit := 0; bit < 8; bit++ {
			d0, s0 := dispatch, softkey
			m.PressKey(b, bit) // sets matrix bit + bc67.1 + b072.14 + IRQ3
			m.bootLoop(4_000_000, nil)
			m.FrontPanel.Release()
			dd, ds := dispatch-d0, softkey-s0
			if dd > 0 || ds > 0 {
				results++
				if results <= 20 {
					t.Logf("  PressKey(byte=%d bit=%d): dispatch(fcn.67c)=%d  softkey(0x1EFDE)=%d", b, bit, dd, ds)
				}
			}
		}
	}
	m.Bus.OnRead = nil
	t.Logf("matrix bits (of 48) that reached the dispatch with gates forced: %d", results)
	if results == 0 {
		t.Log("⇒ even with bc67.1+b072.14 forced, the dispatch never runs — MORE µC bus-master state is gated")
	} else {
		t.Log("⇒ the gates are the blocker; the µC bus-master writes (gate bits) are the unmodeled piece")
	}
}
