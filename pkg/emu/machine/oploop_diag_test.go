package machine

import (
	"fmt"
	"os"
	"sort"
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

// TestADCStaticStatusDiag locks in the faithful static-status fix (analogbus.go):
// with the 0x9A ready/settled bits static (0x06 every read) instead of the old
// "0x00 busy 255/256 reads" cadence, the boot-measurement poll fcn.5e5de no longer
// times out, so the operating loop is freed — far fewer analog reads (0xFFF75E) and
// many more op-loop entries (b010 write @0x1856C) in a late idle window. (History:
// the cadence A/B that proved this — sweeping device.SetADCStatusCadence 256→1 —
// is removed with the knob; cadence 256 gave ~19k reads / 71 entries, cadence 1
// ~735 / 547. The trace gate is UNCHANGED — b0ec/a9a0/b0a1 don't move — so this is
// a faithfulness/perf fix, not the trace fix.) DIAG=1.
func TestADCStaticStatusDiag(t *testing.T) {
	m := diagBootMachine(t)
	m.BootToOperatingWithSweep(260_000_000)

	opEntries, anaReads := 0, 0
	m.Bus.OnWrite = func(addr uint32, sz bus.Size, val uint32) {
		if addr == 0xFFB010 {
			if pc := m.CPU.Reg(cpu.PC); pc >= 0x1856C && pc <= 0x18574 {
				opEntries++
			}
		}
	}
	m.Bus.OnRead = func(addr uint32, sz bus.Size, val uint32) {
		if addr == 0xFFF75E {
			anaReads++
		}
	}
	m.bootLoop(10_000_000, nil) // late idle window
	m.Bus.OnWrite, m.Bus.OnRead = nil, nil

	t.Logf("static status | op-loop entries=%d analogReads=%d | b0ec=0x%X a9a0=0x%04X b0a1=0x%02X",
		opEntries, anaReads, m.Bus.Read(0xFFB0EC, bus.Word), m.Bus.Read(0xFFA9A0, bus.Word),
		byte(m.Bus.Read(0xFFB0A1, bus.Byte)))
	// Regression guard: the static fix should keep analog reads low and the loop busy.
	if anaReads > 5000 {
		t.Errorf("analogReads=%d too high — the 0x9A status cadence regressed (poll timing out)", anaReads)
	}
	if opEntries < 100 {
		t.Errorf("op-loop entries=%d too low — the operating loop is starved", opEntries)
	}
}

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

// TestIdleStackDiag answers the open handoff question: WHO owns the ~9800-read
// analog idle loop? fcn.5e6e8 (completes, returns 0xFFFF uncal) and fcn.5e63c
// (bounded dbra=119) do NOT infinite-loop, so the reads come from a HIGHER-LEVEL
// loop cycling through them. This walks the A6 frame chain at every 0xFFF75E read
// in the idle window and histograms the dominant call STACK — the call chain that
// owns the idle is the routine that must complete/transition. DIAG=1 to run.
func TestIdleStackDiag(t *testing.T) {
	m := diagBootMachine(t)
	m.BootToOperatingWithSweep(260_000_000)

	readPC := map[uint32]int{} // exact PC of the 0xFFF75E read
	caller := map[uint32]int{} // immediate return address (frame 1)
	chainH := map[string]int{} // full A6 chain signature
	samples := 0
	m.Bus.OnRead = func(addr uint32, sz bus.Size, val uint32) {
		if addr != 0xFFF75E {
			return
		}
		samples++
		readPC[m.CPU.Reg(cpu.PC)]++
		// Walk the A6 linked frame chain: each frame is [prev_a6 | ret_addr].
		a6 := m.CPU.Reg(cpu.A6)
		sig := ""
		for depth := 0; depth < 10 && a6 >= 0xFF0000 && a6 < 0xFFF000; depth++ {
			ret := m.Bus.Read(a6+4, bus.Long) & 0xFFFFFF
			if depth == 0 {
				caller[ret]++
			}
			sig += fmt.Sprintf("%05X<", ret)
			next := m.Bus.Read(a6, bus.Long) & 0xFFFFFF
			if next <= a6 || next >= 0xFFF000 {
				break
			}
			a6 = next
		}
		chainH[sig]++
	}
	m.bootLoop(5_000_000, nil)
	m.Bus.OnRead = nil

	t.Logf("0xFFF75E reads in idle window: %d", samples)
	topPCs(t, "read-site PC", readPC, 6)
	topPCs(t, "immediate caller (ret addr)", caller, 8)
	// dominant full chains
	type sc struct {
		s string
		v int
	}
	var cs []sc
	for s, v := range chainH {
		cs = append(cs, sc{s, v})
	}
	sort.Slice(cs, func(i, j int) bool { return cs[i].v > cs[j].v })
	for i, e := range cs {
		if i >= 5 {
			break
		}
		t.Logf("  chain x%d: %s", e.v, e.s)
	}
}

// TestSendCONTSDiag is the DECISIVE lever test for task (b): does delivering a
// real CONTS; command (the faithful HP-IB path) actually set CONTS (b0a1 bit3),
// arm the sweep (a9a0≥0), and draw the trace? If YES, the whole trace blocker
// reduces to "deliver CONTS at power-up" and the fix target is confirmed. If the
// handler runs but a9a0 stays -1 / no vectors, there's a downstream gate (mode
// b0ec) too. Boots with HP-IB installed, captures before/after. DIAG=1.
func TestSendCONTSDiag(t *testing.T) {
	if os.Getenv("DIAG") == "" {
		t.Skip("DIAG=1")
	}
	rom, err := romloader.LoadDir("../../../hp8593a_eeproms")
	if err != nil {
		t.Skip("rom not available")
	}
	m, _ := New8593A(rom)
	m.MMIO.InstallHPIB()
	m.CPU.Reset()
	m.MMIO.SweepActive = true
	m.SweepDrive = true
	m.BootToOperatingWithSweep(250_000_000)
	chip := m.MMIO.Display.Chip
	chip.EnableLineLog()

	b0a1Before := byte(m.Bus.Read(0xFFB0A1, bus.Byte))
	a9a0Before := m.Bus.Read(0xFFA9A0, bus.Word)
	vecBefore := len(chip.LineLog)

	// Track the full command chain: receive (bc28), parse (bc12 FIFO),
	// name-lookup fcn.320fe, DLP scheduler fcn.349b6, dispatcher fcn.12288
	// (b1f8 @0x12290), and the CONTS handler (bchg @0x5f980). Per docs/
	// HPIB_E2E_FLOW.md the 2026-06-02 trace: commands reach the lookup but the
	// handler/scheduler never fires — this re-measures that for CONTS.
	contsHandler, dispatches, fifoWrites, fifoReads := 0, 0, 0, 0
	lookup, scheduler := false, false
	m.Bus.OnWrite = func(addr uint32, sz bus.Size, val uint32) {
		pc := m.CPU.Reg(cpu.PC)
		switch addr {
		case 0xFFB0A1:
			if pc >= 0x5f980 && pc <= 0x5f988 {
				contsHandler++
			}
		case 0xFFB1F8:
			if pc >= 0x12290 && pc <= 0x12296 {
				dispatches++
			}
		case 0xFFBC28:
			fifoWrites++
		}
	}
	m.Bus.OnRead = func(addr uint32, sz bus.Size, val uint32) {
		if addr >= 0xFFBC12 && addr <= 0xFFBC27 { // parser FIFO body
			fifoReads++
		}
		switch pc := m.CPU.Reg(cpu.PC); {
		case pc >= 0x320fe && pc <= 0x32300: // fcn.320fe name-lookup
			lookup = true
		case pc >= 0x349b6 && pc <= 0x349e0: // fcn.349b6 DLP scheduler
			scheduler = true
		}
	}
	// LF (0x0A) is the HP-IB message terminator — NOT ';'. AND the command must be
	// driven IRQ5-only: the sweep STARVES command execution (HPIB_E2E_FLOW.md:118).
	// So: receive via SendHPIB (IRQ4, no sweep), then an IRQ5-only operating drive.
	m.MMIO.GPIB.AddressListener()
	m.SendHPIB([]byte("CONTS\n"), 30_000_000)
	for i := 0; i < 300; i++ { // IRQ5-only operating drive (NO DriveOneSweepChunk)
		m.CPU.Run(100_000)
		m.CPU.SetIRQ(5)
		m.CPU.Run(400)
		m.CPU.SetIRQ(0)
	}
	m.Bus.OnWrite, m.Bus.OnRead = nil, nil
	t.Logf("receive: parser-FIFO writes(bc28)=%d   parse: FIFO reads(bc12)=%d", fifoWrites, fifoReads)
	t.Logf("name-lookup fcn.320fe reached: %v   DLP scheduler fcn.349b6 reached: %v", lookup, scheduler)

	t.Logf("BEFORE: b0a1=0x%02X(bit3=%d) a9a0=0x%04X vec=%d",
		b0a1Before, (b0a1Before>>3)&1, a9a0Before, vecBefore)
	t.Logf("AFTER : b0a1=0x%02X(bit3=%d) a9a0=0x%04X vec=%d",
		byte(m.Bus.Read(0xFFB0A1, bus.Byte)), (byte(m.Bus.Read(0xFFB0A1, bus.Byte))>>3)&1,
		m.Bus.Read(0xFFA9A0, bus.Word), len(chip.LineLog))
	t.Logf("CONTS handler (bchg @0x5f980) reached: %d   command dispatches during send: %d",
		contsHandler, dispatches)
	t.Logf("b0ec=0x%X (spectrum=0x2D/0x31/0x36)", m.Bus.Read(0xFFB0EC, bus.Word))
	t.Log("⇒ if bit3 flips 0→1 and a9a0 arms, CONTS IS the lever and 'deliver at power-up' is the fix")
}

// TestB0A1WritersDiag logs EVERY writer of b0a1 (the sweep-mode byte, bit3=CONTS)
// across the whole boot — PC + value. The ROM grep shows only bit-ops (bset/bclr/
// bchg) touch b0a1, none of them bit3 except fcn.5f968's bchg @0x5f980. But a default-
// STATE block-copy (movem/move (a0)+ into the b0a0 region) would NOT show in a b0a1
// grep. If a write from a non-bit-op PC sets b0a1 (esp. with bit3), continuous-sweep
// comes from a state template — find/seed it. If the ONLY writers are the known
// bit-ops and bit3 is never set, CONTS truly needs the command path. DIAG=1.
func TestB0A1WritersDiag(t *testing.T) {
	m := diagBootMachine(t)
	type w struct{ pc, val uint32 }
	writes := map[uint32]int{} // PC histogram
	var bit3Sets []w
	var samples []w
	m.Bus.OnWrite = func(addr uint32, sz bus.Size, val uint32) {
		// b0a1 is byte 1 of the b0a0 word; a word/long write to b0a0 also hits it.
		if addr != 0xFFB0A1 && !(addr == 0xFFB0A0 && sz != bus.Byte) {
			return
		}
		pc := m.CPU.Reg(cpu.PC)
		writes[pc]++
		// capture the resulting b0a1 byte after the write
		b3 := byte(m.Bus.Read(0xFFB0A1, bus.Byte)) & 0x08
		if b3 != 0 || (addr == 0xFFB0A0 && sz != bus.Byte) {
			rec := w{pc, val}
			if b3 != 0 && len(bit3Sets) < 12 {
				bit3Sets = append(bit3Sets, rec)
			}
		}
		if len(samples) < 24 {
			samples = append(samples, w{pc, val})
		}
	}
	m.BootToOperatingWithSweep(260_000_000)
	m.Bus.OnWrite = nil

	topPCs(t, "b0a1/b0a0 writer PCs", writes, 12)
	t.Logf("writes that left b0a1 bit3 SET: %d", len(bit3Sets))
	for _, r := range bit3Sets {
		t.Logf("  bit3-set from PC=0x%X val=0x%X", r.pc, r.val)
	}
	t.Logf("FINAL b0a1=0x%02X b0a0=0x%04X", byte(m.Bus.Read(0xFFB0A1, bus.Byte)), m.Bus.Read(0xFFB0A0, bus.Word))
	t.Log("⇒ a non-bit-op writer (block copy) ⇒ state-template path; only bit-ops + bit3 never set ⇒ needs command path")
}

// TestCmdDispatchDiag logs every command the dispatcher fcn.12288 processes during
// boot. fcn.12288's 2nd-ish instruction `0x12290 bset #12,0xB1F8` runs on EVERY entry;
// at that point the command code is at a6+8 and the dispatch index is (code>>8)-0xd.
// This settles the CONTS-delivery question: if the dispatcher runs MANY commands at
// boot but never the CONTS code, the command path is alive and the power-up default
// config simply omits/never-reaches CONTS (a config/state issue); if it runs 0×, the
// executor is never fed at boot (a different gap). The CONTS handler is the (code>>8)
// whose case is 0x126d8. DIAG=1.
func TestCmdDispatchDiag(t *testing.T) {
	m := diagBootMachine(t)
	codeHi := map[uint32]int{} // histogram of (code>>8) dispatch classes
	codeFull := map[uint32]int{}
	total := 0
	m.Bus.OnWrite = func(addr uint32, sz bus.Size, val uint32) {
		if addr != 0xFFB1F8 {
			return
		}
		if pc := m.CPU.Reg(cpu.PC); pc >= 0x12290 && pc <= 0x12296 {
			code := m.Bus.Read(m.CPU.Reg(cpu.A6)+8, bus.Word) & 0xFFFF
			codeHi[code>>8]++
			codeFull[code]++
			total++
		}
	}
	m.BootToOperatingWithSweep(260_000_000)
	m.Bus.OnWrite = nil

	t.Logf("fcn.12288 command dispatches during boot: %d (distinct codes=%d)", total, len(codeFull))
	topPCs(t, "dispatch class (code>>8)", codeHi, 16)
	topPCs(t, "full command code", codeFull, 16)
	t.Log("⇒ many dispatches but no CONTS code ⇒ power-up config omits/never-reaches CONTS; 0 ⇒ executor never fed")
}

// TestIdleStackScanDiag resolves the DERAIL-vs-STEPPED ambiguity: is the stuck
// analog DLP at 0xFC9A32 a derail (firmware jumped there, can't return to dispatch
// CONTS) or the FOREGROUND DLP being stepped by the operating loop fcn.18568 (so
// fcn.18568/fcn.34EE8 sit ABOVE it on the stack)? The a6 chain breaks at the DLP
// boundary, so walk the RAW A7 stack for ROM return addresses in the operating
// loop (0x18568..0x18B00), DLP interpreter (0x34EE8..0x35200), and DLP scheduler
// (0x34690..0x34700). If present ⇒ stepped-by-op-loop (blocker is downstream);
// if absent ⇒ derail. DIAG=1.
func TestIdleStackScanDiag(t *testing.T) {
	m := diagBootMachine(t)
	m.BootToOperatingWithSweep(260_000_000)

	type rng struct {
		name   string
		lo, hi uint32
		seen   int
	}
	rngs := []rng{
		{"opLoop(18568)", 0x18568, 0x18B00, 0},
		{"dlpInterp(34EE8)", 0x34EE8, 0x35200, 0},
		{"dlpSched(34690)", 0x34690, 0x34700, 0},
		{"cmdDisp(12288)", 0x12288, 0x12800, 0},
		{"cmdExec(12b10)", 0x12B10, 0x12D00, 0},
	}
	samples := 0
	m.Bus.OnRead = func(addr uint32, sz bus.Size, val uint32) {
		if addr != 0xFFF75E || samples >= 200 {
			return
		}
		samples++
		sp := m.CPU.Reg(cpu.A7)
		for off := uint32(0); off < 0x600; off += 2 {
			w := m.Bus.Read(sp+off, bus.Long) & 0xFFFFFF
			for i := range rngs {
				if w >= rngs[i].lo && w < rngs[i].hi {
					rngs[i].seen++
				}
			}
		}
	}
	m.bootLoop(2_000_000, nil)
	m.Bus.OnRead = nil

	t.Logf("stack-scan over %d idle analog-reads (ret-addrs found in range, summed):", samples)
	for _, r := range rngs {
		verdict := "ABSENT"
		if r.seen > 0 {
			verdict = "PRESENT"
		}
		t.Logf("  %-18s [0x%05X..0x%05X): %-7s (hits=%d)", r.name, r.lo, r.hi, verdict, r.seen)
	}
	t.Log("⇒ opLoop/dlpInterp PRESENT ⇒ stepped by op-loop (blocker downstream); ABSENT ⇒ derail")
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
