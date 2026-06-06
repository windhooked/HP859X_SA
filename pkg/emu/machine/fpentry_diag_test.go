package machine

import (
	"os"
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
)

// TestFrontPanelEntryDiag answers "how do front-panel commands enter the firmware
// loop?" empirically. IRQ3 handler 0x2B1E only SETS bc67.0 + ACKs; the operating
// loop must CONSUME it at 0x18F42 (bclr bc67.0) and dispatch the softkey (slot
// 0x244 -> fcn.1EFDE). Inject a key, drive long (operating loop running), and check
// whether the consume + softkey dispatch are ever reached. DIAG=1.
func TestFrontPanelEntryDiag(t *testing.T) {
	if os.Getenv("DIAG") == "" {
		t.Skip("DIAG=1")
	}
	m := diagBootMachine(t)
	m.BootToOperatingWithSweep(250_000_000)

	irq3set, consume, softkey := 0, 0, 0
	m.Bus.OnWrite = func(addr uint32, sz bus.Size, val uint32) {
		if addr != 0xFFBC67 {
			return
		}
		switch pc := m.CPU.Reg(cpu.PC); {
		case pc >= 0x2b26 && pc <= 0x2b2c: // IRQ3 handler bset bc67.0
			irq3set++
		case pc >= 0x18f42 && pc <= 0x18f48: // operating-loop consume bclr bc67.0
			consume++
		}
	}
	m.Bus.OnRead = func(addr uint32, sz bus.Size, val uint32) {
		if pc := m.CPU.Reg(cpu.PC); pc >= 0x1efde && pc <= 0x1f100 { // softkey/menu dispatch
			softkey++
		}
	}

	// Inject a front-panel key and drive the operating loop long, re-raising IRQ3
	// while the front panel has the event pending.
	m.FrontPanel.SetBit(0, 0)
	for i := 0; i < 400; i++ {
		if m.FrontPanel.Pending() {
			m.CPU.SetIRQ(3)
			m.CPU.Run(bootIRQServiceCost)
			m.CPU.SetIRQ(0)
		}
		m.bootLoop(2_000_000, nil)
	}
	m.Bus.OnWrite, m.Bus.OnRead = nil, nil

	t.Logf("IRQ3 handler set bc67.0: %d   operating-loop CONSUME (0x18F42 bclr): %d   softkey dispatch (0x1EFDE): %d",
		irq3set, consume, softkey)
	t.Logf("FrontPanel.Consumed()=%v  bc67=0x%02X", m.FrontPanel.Consumed(), byte(m.Bus.Read(0xFFBC67, bus.Byte)))
	if consume == 0 {
		t.Log("⇒ front-panel keys are SIGNALED (IRQ3→bc67.0) but NEVER CONSUMED — the consume/softkey path is never reached")
	}
}
