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

// sweeparm_diag_test.go — DIAG-gated probes for the sweep-arm / operating-loop
// deadlock (docs/TRACE_DISPLAY_PATH.md "WHY a9a0 SETTLES -1"). Run with DIAG=1.
// Together they establish the full chain from the un-drawn trace / un-repainted
// graticule to its root:
//
//	graticule eroded ← fcn.18568 (operating loop) never entered
//	  ← orchestrator (0x17460 tst a9a0; blt) only hands off when a9a0 >= 0
//	  ← a9a0 = -1 because b0a1 bit 3 (CONTS mode) is clear (fcn.8f04 @ 0x8f5a)
//	  ← the CONTS handler fcn.5f968 never runs (only reachable from the op loop)
//	  ← DEADLOCK; forcing b0a1 bit3 arms the slow sweep (a9a0=252) but is NOT
//	    sufficient to break into fcn.18568.
//
// Key cells: a9a0=0xFFA9A0 (sweep point counter), b0a1=0xFFB0A1 (sweep-mode byte,
// bit3 = continuous), befa=0xFFBEFA (bit13 = sweep-done), bf34=0xFFBF34 (IRQ6
// capture dispatch).

func diagBootMachine(t *testing.T) *Machine {
	t.Helper()
	if os.Getenv("DIAG") == "" {
		t.Skip("set DIAG=1 to run the sweep-arm diagnosis probes")
	}
	rom, err := romloader.LoadDir("../../../hp8593a_eeproms")
	if err != nil {
		t.Skip("rom not available")
	}
	m, _ := New8593A(rom)
	m.CPU.Reset()
	m.MMIO.SweepActive = true
	m.SweepDrive = true
	return m
}

// topPCs logs the busiest entries of a PC/value histogram.
func topPCs(t *testing.T, label string, mp map[uint32]int, n int) {
	t.Helper()
	type kv struct {
		k uint32
		v int
	}
	var ks []kv
	for k, v := range mp {
		ks = append(ks, kv{k, v})
	}
	sort.Slice(ks, func(i, j int) bool { return ks[i].v > ks[j].v })
	s := ""
	for i, e := range ks {
		if i >= n {
			break
		}
		s += fmt.Sprintf(" 0x%X=%d", e.k, e.v)
	}
	t.Logf("%s:%s", label, s)
}

// TestSweepArmDiag — ROOT CAUSE: every steady-state write to a9a0 is 0xFFFF
// (disable) from 0x92B2, reached from 0x8F5A `btst #3,b0a1; beq 0x92b2` with b0a1
// bit 3 clear. The fast (<20 ms) path arms a9a0=20 (0x90C8) bypassing the gate.
func TestSweepArmDiag(t *testing.T) {
	m := diagBootMachine(t)
	type rec struct {
		pc, val, in      uint32
		b068, b06c       uint32
		b0ec, b0e6, b0e8 uint16
		b0a1             byte
	}
	pcCount, valCount := map[uint32]int{}, map[uint32]int{}
	var last []rec
	m.Bus.OnWrite = func(addr uint32, sz bus.Size, val uint32) {
		if addr != 0xFFA9A0 {
			return
		}
		a6 := m.CPU.Reg(cpu.A6)
		r := rec{
			pc: m.CPU.Reg(cpu.PC), val: val & 0xffff,
			in:   m.Bus.Read(a6-4, bus.Long),
			b068: m.Bus.Read(0xFFB068, bus.Long), b06c: m.Bus.Read(0xFFB06C, bus.Long),
			b0ec: uint16(m.Bus.Read(0xFFB0EC, bus.Word)), b0e6: uint16(m.Bus.Read(0xFFB0E6, bus.Word)),
			b0e8: uint16(m.Bus.Read(0xFFB0E8, bus.Word)), b0a1: byte(m.Bus.Read(0xFFB0A1, bus.Byte)),
		}
		pcCount[r.pc]++
		valCount[r.val]++
		if last = append(last, r); len(last) > 12 {
			last = last[1:]
		}
	}
	m.BootToOperatingWithSweep(260_000_000)
	m.Bus.OnWrite = nil

	topPCs(t, "a9a0 write-site PC histogram (write+6 ⇒ 0x92B8 means 0x92B2 disable)", pcCount, 8)
	topPCs(t, "a9a0 value histogram", valCount, 8)
	for _, r := range last {
		t.Logf("  PC=0x%05X a9a0<-0x%04X in=0x%X b068=0x%X b0ec=0x%X b0e6=0x%X b0a1=0x%02X(bit3=%d)",
			r.pc, r.val, r.in, r.b068, r.b0ec, r.b0e6, r.b0a1, (r.b0a1>>3)&1)
	}
	t.Logf("FINAL a9a0=0x%04X b0a1=0x%02X bf34=0x%X",
		m.Bus.Read(0xFFA9A0, bus.Word), m.Bus.Read(0xFFB0A1, bus.Byte), m.Bus.Read(0xFFBF34, bus.Long))
}

// TestSweepDeadlockDiag — the DEADLOCK: the CONTS handler fcn.5f968 (b0a1 read @
// ~0x5F96E) and the operating loop fcn.18568 (b0a1 write @ ~0x1933A) are BOTH never
// reached, and the orchestrator (a9a0 reads at 0x173xx-0x174xx) only ever sees
// a9a0=-1 except for the brief fast-arm. Sweeps do complete (befa bit13 fires).
func TestSweepDeadlockDiag(t *testing.T) {
	m := diagBootMachine(t)
	contsHandler, opLoop, befaDone, orchArmed := 0, 0, 0, 0
	rdPC, orchVal := map[uint32]int{}, map[uint32]int{}
	m.Bus.OnRead = func(addr uint32, sz bus.Size, val uint32) {
		switch addr {
		case 0xFFB0A1:
			pc := m.CPU.Reg(cpu.PC)
			rdPC[pc]++
			if pc >= 0x5F968 && pc <= 0x5F990 {
				contsHandler++
			}
		case 0xFFA9A0:
			if pc := m.CPU.Reg(cpu.PC); pc >= 0x17300 && pc <= 0x17500 {
				orchVal[val&0xffff]++
				if v := val & 0xffff; v != 0xFFFF && v != 0 {
					orchArmed++
				}
			}
		}
	}
	m.Bus.OnWrite = func(addr uint32, sz bus.Size, val uint32) {
		switch {
		case addr == 0xFFB0A1 && m.CPU.Reg(cpu.PC) >= 0x18568 && m.CPU.Reg(cpu.PC) <= 0x1A000:
			opLoop++
		case addr == 0xFFBEFA && val&0x2000 != 0:
			befaDone++
		}
	}
	m.BootToOperatingWithSweep(260_000_000)
	m.Bus.OnRead, m.Bus.OnWrite = nil, nil

	topPCs(t, "b0a1 READ PCs", rdPC, 8)
	topPCs(t, "a9a0 value seen by orchestrator", orchVal, 6)
	t.Logf("CONTS handler fcn.5f968 reached: %d   operating loop fcn.18568 reached: %d", contsHandler, opLoop)
	t.Logf("orchestrator saw a9a0 ARMED: %d   sweep-DONE (befa b13) set: %d", orchArmed, befaDone)
	t.Logf("FINAL a9a0=0x%04X b0a1=0x%02X befa=0x%04X", m.Bus.Read(0xFFA9A0, bus.Word),
		m.Bus.Read(0xFFB0A1, bus.Byte), m.Bus.Read(0xFFBEFA, bus.Word))
}

// TestForceContModeDiag — LEVER CONFIRMATION via the RELIABLE signal (vectors
// drawn, not the unreliable b0a1-write op-loop detector). Holding b0a1 bit3 (CONTS)
// across the boot makes fcn.8f04 arm the SLOW full-span sweep (a9a0=252, vs -1/20
// without it). The decisive test is whether that renders the OPERATING UI — measured
// as vectors drawn (chip.LineLog), baseline (no force) vs b0a1-bit3-held. (Firmware
// bit-0 ops on b0a1 preserve bit3, so setting it per-segment sticks.)
func TestForceContModeDiag(t *testing.T) {
	run := func(force bool) (maxArm uint32, vectors int) {
		m := diagBootMachine(t)
		chip := m.MMIO.Display.Chip
		chip.EnableLineLog()
		m.Bus.OnWrite = func(addr uint32, sz bus.Size, val uint32) {
			if addr == 0xFFA9A0 {
				if v := val & 0xffff; v != 0xFFFF && v != 0 && v != 0x5555 && v != 0xAAAA && v > maxArm {
					maxArm = v
				}
			}
		}
		const seg = 5_000_000
		for done := 0; done < 260_000_000; done += seg {
			if force {
				b := byte(m.Bus.Read(0xFFB0A1, bus.Byte)) | 0x08
				m.Bus.Write(0xFFB0A1, bus.Byte, uint32(b))
			}
			m.BootToOperatingWithSweep(seg)
			vectors += len(chip.LineLog)
			chip.LineLog = chip.LineLog[:0]
		}
		m.Bus.OnWrite = nil
		return maxArm, vectors
	}
	baseArm, baseVec := run(false)
	forceArm, forceVec := run(true)
	t.Logf("NO force:        max a9a0 arm=0x%-4X  vectors drawn=%d", baseArm, baseVec)
	t.Logf("b0a1 bit3 HELD:  max a9a0 arm=0x%-4X  vectors drawn=%d", forceArm, forceVec)
	t.Logf("⇒ if forced ≫ baseline, forcing CONTS mode renders the operating UI ⇒ bit3 IS the lever")
}

// TestSweepArmPersistDiag — FIX-PATH experiment: hold b0a1 bit3 (so fcn.8f04 arms
// the slow sweep, value computed naturally) AND keep that slow arm from reverting to
// -1 (re-write the last armed count whenever the firmware writes -1). If the missing
// piece is purely arm PERSISTENCE, the 0x17400 orchestrator should now hand off and
// fcn.18568 (b0a1 write @ 0x18xxx) should be reached. Distinguishes "needs the slow
// arm to persist" from "there's yet another downstream gate".
func TestSweepArmPersistDiag(t *testing.T) {
	m := diagBootMachine(t)
	var lastArm uint32
	opLoop := 0
	m.Bus.OnWrite = func(addr uint32, sz bus.Size, val uint32) {
		switch addr {
		case 0xFFA9A0:
			v := val & 0xffff
			switch {
			case v != 0xFFFF && v != 0 && v != 0x5555 && v != 0xAAAA:
				lastArm = v
			case v == 0xFFFF && lastArm >= 200:
				m.Bus.Write(0xFFA9A0, bus.Word, lastArm) // keep the slow sweep armed
			}
		case 0xFFB0A1:
			if pc := m.CPU.Reg(cpu.PC); pc >= 0x18568 && pc <= 0x1A000 {
				opLoop++
			}
		}
	}
	const seg = 5_000_000
	for done := 0; done < 260_000_000; done += seg {
		b := byte(m.Bus.Read(0xFFB0A1, bus.Byte)) | 0x08
		m.Bus.Write(0xFFB0A1, bus.Byte, uint32(b))
		m.BootToOperatingWithSweep(seg)
	}
	m.Bus.OnWrite = nil

	t.Logf("b0a1 bit3 HELD + slow arm PERSISTED (lastArm=0x%X): fcn.18568 reached %d times", lastArm, opLoop)
	t.Logf("FINAL a9a0=0x%04X b0a1=0x%02X befa=0x%04X bf34=0x%X",
		m.Bus.Read(0xFFA9A0, bus.Word), m.Bus.Read(0xFFB0A1, bus.Byte),
		m.Bus.Read(0xFFBEFA, bus.Word), m.Bus.Read(0xFFBF34, bus.Long))
}

// TestForceArmReproDiag — reproduces the historic force-arm (force a9a0=0x190 + befa
// bit13 persistently POST-boot) to confirm the fix TARGET is still reachable in this
// baseline: if it drives the firmware into fcn.18568, the model just has to PRODUCE
// that state naturally (lock-step a9a0 + befa). Reports op-loop entries + vectors
// drawn (the operating UI).
func TestForceArmReproDiag(t *testing.T) {
	m := diagBootMachine(t)
	m.BootToOperatingWithSweep(260_000_000)
	chip := m.MMIO.Display.Chip
	chip.EnableLineLog()
	linesBefore := len(chip.LineLog)

	opLoop := 0
	m.Bus.OnWrite = func(addr uint32, sz bus.Size, val uint32) {
		if addr == 0xFFB0A1 {
			if pc := m.CPU.Reg(cpu.PC); pc >= 0x18568 && pc <= 0x1A000 {
				opLoop++
			}
		}
	}
	for i := 0; i < 300; i++ {
		m.Bus.Write(0xFFA9A0, bus.Word, 0x190)                                 // force full point count
		m.Bus.Write(0xFFBEFA, bus.Word, m.Bus.Read(0xFFBEFA, bus.Word)|0x2000) // befa bit13 = sweep-done
		m.bootLoop(200_000, nil)
	}
	m.Bus.OnWrite = nil

	t.Logf("force a9a0=0x190 + befa b13 (post-boot, persistent): fcn.18568 reached %d times, vectors drawn +%d",
		opLoop, len(chip.LineLog)-linesBefore)
	t.Logf("FINAL a9a0=0x%04X PC=0x%X", m.Bus.Read(0xFFA9A0, bus.Word), m.CPU.Reg(cpu.PC))
}

// TestKickstartDiag — the FIX experiment: KICK the sweep (force a9a0=0x190 + befa
// bit13 for a short window) to break the deadlock into fcn.18568, then STOP forcing
// and see whether the firmware SELF-SUSTAINS — i.e. did the operating loop set CONTS
// (b0a1 bit3) and keep the slow sweep arming + drawing on its own? If yes, the fix is
// a bounded boot kickstart; if it reverts, the operating loop doesn't latch CONTS and
// the full lock-step sweep model is required.
func TestKickstartDiag(t *testing.T) {
	m := diagBootMachine(t)
	m.BootToOperatingWithSweep(260_000_000)
	chip := m.MMIO.Display.Chip
	chip.EnableLineLog()

	// Phase 1 — KICKSTART (force armed+done for ~20M cycles).
	for i := 0; i < 100; i++ {
		m.Bus.Write(0xFFA9A0, bus.Word, 0x190)
		m.Bus.Write(0xFFBEFA, bus.Word, m.Bus.Read(0xFFBEFA, bus.Word)|0x2000)
		m.bootLoop(200_000, nil)
	}
	kickVec := len(chip.LineLog)
	kickB0a1 := byte(m.Bus.Read(0xFFB0A1, bus.Byte))
	chip.LineLog = chip.LineLog[:0]

	// Phase 2 — STOP forcing; does it self-sustain (keep arming + drawing)?
	maxA9a0 := uint32(0)
	m.Bus.OnWrite = func(addr uint32, sz bus.Size, val uint32) {
		if addr == 0xFFA9A0 {
			if v := val & 0xffff; v != 0xFFFF && v != 0 && v != 0x5555 && v != 0xAAAA && v > maxA9a0 {
				maxA9a0 = v
			}
		}
	}
	for i := 0; i < 100; i++ {
		m.bootLoop(200_000, nil)
	}
	m.Bus.OnWrite = nil
	selfVec := len(chip.LineLog)
	selfB0a1 := byte(m.Bus.Read(0xFFB0A1, bus.Byte))

	t.Logf("KICKSTART (forcing):     vectors=%d  b0a1=0x%02X(bit3=%d)", kickVec, kickB0a1, (kickB0a1>>3)&1)
	t.Logf("SELF-SUSTAIN (force off): vectors=%d  b0a1=0x%02X(bit3=%d)  max a9a0 arm=0x%X  a9a0=0x%04X",
		selfVec, selfB0a1, (selfB0a1>>3)&1, maxA9a0, m.Bus.Read(0xFFA9A0, bus.Word))
	t.Logf("⇒ if SELF-SUSTAIN vectors stay high and b0a1 bit3 set, a boot kickstart fixes it")
}
