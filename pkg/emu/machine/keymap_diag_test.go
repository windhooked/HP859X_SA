package machine

import (
	"os"
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
)

// TestKeyMapProbeDiag — decisive front-panel key test: combine the µC bus-master
// GATES (bc67.1 + b072.14, which the firmware never sets) with a BCD-coded key
// value in the frame (fcn.59ef0 converts frame nibbles to ASCII digits → parser).
// Sweep BCD key codes 0..99 and look for per-key differentiation: the softkey/menu
// dispatch (0x1EFDE), the command name-lookup (fcn.320fe), or the parser FIFO
// (bc12) advancing — any of which means the key was RECOGNIZED. DIAG=1.
func TestKeyMapProbeDiag(t *testing.T) {
	if os.Getenv("DIAG") == "" {
		t.Skip("DIAG=1")
	}
	m := diagBootMachine(t)
	m.BootToOperatingWithSweep(250_000_000)

	softkey, lookup, fifo := 0, 0, 0
	m.Bus.OnRead = func(addr uint32, sz bus.Size, val uint32) {
		if addr >= 0xFFBC12 && addr <= 0xFFBC27 {
			fifo++
		}
		switch pc := m.CPU.Reg(cpu.PC); {
		case pc >= 0x1efde && pc <= 0x1f100:
			softkey++
		case pc >= 0x320fe && pc <= 0x32300:
			lookup++
		}
	}

	setGates := func() {
		m.Bus.Write(0xFFBC67, bus.Byte, uint32(byte(m.Bus.Read(0xFFBC67, bus.Byte))|0x02))
		m.Bus.Write(0xFFB072, bus.Word, m.Bus.Read(0xFFB072, bus.Word)|0x4000)
	}
	hits := 0
	for n := 0; n <= 99; n++ {
		bcd := byte((n/10)<<4 | (n % 10))
		sk0, lk0, ff0 := softkey, lookup, fifo
		m.FrontPanel.InjectMatrix([6]byte{bcd})
		setGates()
		m.CPU.SetIRQ(3)
		m.CPU.Run(bootIRQServiceCost)
		m.CPU.SetIRQ(0)
		setGates() // re-assert in case the handler cleared them
		m.bootLoop(4_000_000, nil)
		m.FrontPanel.Release()
		dsk, dlk, dff := softkey-sk0, lookup-lk0, fifo-ff0
		if dsk > 0 || dff > 5 {
			hits++
			if hits <= 25 {
				t.Logf("  key BCD=%02d (0x%02X): softkey=%d lookup=%d fifoΔ=%d", n, bcd, dsk, dlk, dff)
			}
		}
	}
	m.Bus.OnRead = nil
	t.Logf("BCD key codes (of 100) that produced a recognized action: %d", hits)
	if hits == 0 {
		t.Log("⇒ even with gates + BCD encoding, no per-key recognition — the µC writes MORE than the 2 gates")
	}
}
