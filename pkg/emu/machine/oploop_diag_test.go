package machine

import (
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
)

// oploop_diag_test.go — DIAG-gated probes for the operating-loop / DLP-render
// blocker (docs/DRIVETICK_BLOCKER.md). Run with DIAG=1.
//
// TestOpLoopEntryDiag RE-MEASURES the foundational question with a RELIABLE signal:
// is the operating loop fcn.18568 actually entered during boot? The earlier
// "b0a1-write @0x1933A" detector was unreliable (the loop renders without always
// hitting that conditional deep-path bclr). fcn.18568's 2nd instruction is
// `0x1856C move.w f300,b010` — executed on EVERY entry — so a b010 write from that
// PC is a reliable entry count. Decides the direction: if entered but not
// sustaining ⇒ the deep-path block within the loop; if never entered ⇒ the
// sweep-arm entry deadlock.
func TestOpLoopEntryDiag(t *testing.T) {
	m := diagBootMachine(t)
	entries := 0
	cmdExec := 0 // jsr fcn.12b10 @ 0x183B6 region (command executor inside fcn.18568)
	m.Bus.OnWrite = func(addr uint32, sz bus.Size, val uint32) {
		if addr != 0xFFB010 {
			return
		}
		if pc := m.CPU.Reg(cpu.PC); pc >= 0x1856C && pc <= 0x18574 {
			entries++
		}
	}
	// fcn.12b10 entry writes its frame; detect via the command-record cell it reads.
	// Simpler: count entries; cmdExec stays a placeholder for a follow-up signal.
	_ = cmdExec
	m.BootToOperatingWithSweep(260_000_000)
	m.Bus.OnWrite = nil

	t.Logf("fcn.18568 entries (b010 write @0x1856C, RELIABLE): %d", entries)
	t.Logf("FINAL PC=0x%X a9a0=0x%04X b0a1=0x%02X",
		m.CPU.Reg(cpu.PC), m.Bus.Read(0xFFA9A0, bus.Word), m.Bus.Read(0xFFB0A1, bus.Byte))
	if entries == 0 {
		t.Log("⇒ fcn.18568 NEVER entered ⇒ entry deadlock (sweep-arm), not a deep-path block")
	} else {
		t.Log("⇒ fcn.18568 IS entered ⇒ the block is INSIDE the loop (DRIVETICK deep path), not entry")
	}
}
