package machine

import (
	"fmt"
	"sort"
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

// TestCalGatesMeasureModeDiag is the DECISIVE check (option c): does cal-validity /
// cal data gate the measure mode at all? A/B: boot cal-INVALID (blank NVRAM →
// startup checksum FAILS) vs cal-VALID (the default Synthesize → checksum passes),
// and compare the measure-mode cells (0xB0EC mode, a9a0 sweep-arm, b0a1 CONTS) +
// vectors drawn. Also counts NON-checksum reads of the cal NVRAM (0x200000) — if
// the only cal reads are the checksum loop (0x454A) and the A/B cells are identical,
// cal is PROVEN irrelevant to the measure-mode blocker. DIAG=1 to run.
func TestCalGatesMeasureModeDiag(t *testing.T) {
	run := func(blankCal bool) string {
		m := diagBootMachine(t)
		if blankCal {
			m.CalNVRAM.LoadImage([]byte{}) // all-zero ⇒ Σ≠1 ⇒ checksum FAILS ⇒ "USING DEFAULTS"
		}
		nonChecksumCalReads := 0
		calReadPC := map[uint32]int{}
		m.Bus.OnRead = func(addr uint32, sz bus.Size, val uint32) {
			if addr >= 0x200000 && addr <= 0x20FFFF {
				if pc := m.CPU.Reg(cpu.PC); pc < 0x4540 || pc > 0x4580 { // exclude checksum loop
					nonChecksumCalReads++
					calReadPC[pc]++
				}
			}
		}
		chip := m.MMIO.Display.Chip
		chip.EnableLineLog()
		vectors := 0
		const seg = 5_000_000
		for done := 0; done < 260_000_000; done += seg {
			m.BootToOperatingWithSweep(seg)
			vectors += len(chip.LineLog)
			chip.LineLog = chip.LineLog[:0]
		}
		m.Bus.OnRead = nil
		return fmt.Sprintf("b0ec=0x%X a9a0=0x%04X b0a1=0x%02X bb2c=0x%04X vectors=%d nonChecksumCalReads=%d (from %d PCs)",
			m.Bus.Read(0xFFB0EC, bus.Word), m.Bus.Read(0xFFA9A0, bus.Word),
			byte(m.Bus.Read(0xFFB0A1, bus.Byte)), m.Bus.Read(0xFFBB2C, bus.Word),
			vectors, nonChecksumCalReads, len(calReadPC))
	}
	t.Logf("cal-INVALID (blank): %s", run(true))
	t.Logf("cal-VALID   (synth): %s", run(false))
	t.Log("⇒ if the measure-mode cells (b0ec/a9a0/b0a1) match, cal-validity does NOT gate the measure mode")
}

// TestIdleLoopDiag finds the DOMINANT post-boot idle loop in the SWEEP-DRIVEN boot
// (where fcn.18568 IS entered, unlike the doc's passive boot) and which gate flag it
// spins on. The measure-mode setter fcn.21c96 is called from the measurement state
// machine 0x22xxx, which (per a7iobus.go) waits on the sweep-event IRQ handshake RAM
// flags. This pins the exact stuck loop + gate. DIAG=1 to run.
func TestIdleLoopDiag(t *testing.T) {
	m := diagBootMachine(t)
	m.BootToOperatingWithSweep(260_000_000)

	pcPage := map[uint32]int{}
	gateRead := map[uint32]int{}
	m.Bus.OnRead = func(addr uint32, sz bus.Size, val uint32) {
		pcPage[m.CPU.Reg(cpu.PC)>>10]++ // 1KB-page PC histogram
		switch addr {
		case 0xFFBF26, 0xFFB1E0, 0xFFB212, 0xFFAD7D, 0xFFF72A, 0xFFF300, 0xFFF75E:
			gateRead[addr]++
		}
	}
	m.bootLoop(5_000_000, nil) // idle window (sweep-driving still on)
	m.Bus.OnRead = nil

	type kv struct {
		k uint32
		v int
	}
	top := func(label string, mp map[uint32]int, shift int) {
		var ks []kv
		for k, v := range mp {
			ks = append(ks, kv{k, v})
		}
		sort.Slice(ks, func(i, j int) bool { return ks[i].v > ks[j].v })
		s := ""
		for i, e := range ks {
			if i >= 8 {
				break
			}
			s += fmt.Sprintf(" 0x%X=%d", e.k<<shift, e.v)
		}
		t.Logf("%s:%s", label, s)
	}
	top("dominant idle PC 1KB-pages", pcPage, 10)
	top("gate-flag/poll reads", gateRead, 0)
	t.Logf("FINAL PC=0x%X b0ec=0x%X a9a0=0x%04X b1e0=0x%X b212=0x%X ad7d=0x%X bf26=0x%X",
		m.CPU.Reg(cpu.PC), m.Bus.Read(0xFFB0EC, bus.Word), m.Bus.Read(0xFFA9A0, bus.Word),
		m.Bus.Read(0xFFB1E0, bus.Word), m.Bus.Read(0xFFB212, bus.Word),
		byte(m.Bus.Read(0xFFAD7D, bus.Byte)), m.Bus.Read(0xFFBF26, bus.Long))
}
