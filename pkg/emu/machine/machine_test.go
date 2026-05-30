package machine_test

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/windhooked/HP859X_SA/internal/emutest"
	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
	"github.com/windhooked/HP859X_SA/pkg/emu/machine"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

// eepromDir returns the absolute path to hp8593a_eeproms/ regardless of where
// the test binary is run from.
func eepromDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "../../../hp8593a_eeproms")
}

func newMachine(t *testing.T) *machine.Machine {
	t.Helper()
	rom, err := romloader.LoadDir(eepromDir(t))
	if err != nil {
		t.Fatalf("romloader.LoadDir: %v", err)
	}
	m, err := machine.New8593A(rom)
	if err != nil {
		t.Fatalf("New8593A: %v", err)
	}
	m.CPU.Reset()
	return m
}

// TestMachineResetVector verifies the bus-backed machine loads the correct
// reset vector via the ROM region (built from the four Rev L Intel-HEX dumps).
func TestMachineResetVector(t *testing.T) {
	m := newMachine(t)
	if got := m.CPU.Reg(cpu.PC); got != 0x00001B34 {
		t.Errorf("PC = %#08X, want 0x00001B34 (Rev L)", got)
	}
	if got := m.CPU.Reg(cpu.A7); got != 0x00FF948A {
		t.Errorf("A7 = %#08X, want 0x00FF948A (Rev L)", got)
	}
	if got := m.CPU.Reg(cpu.SR); got != 0x00002700 {
		t.Errorf("SR = %#08X, want 0x00002700", got)
	}
}

// TestMachineBootMilestone_50 — Phase-1 gate: the machine must execute at
// least 50 boot instructions from reset without errors and end up in ROM.
//
// Rev L boot prologue (empirically): 0x1B34→1B3C are three MOVEA setup
// instructions, then BRA to 0x3998 ORI #$700,SR (supervisor enter), then
// hardware init at 0x399C+. At step 50 PC is around 0x4426 in the early
// register-zero / hardware-poll block.
//
// The test deliberately doesn't assert a specific landing PC — that's fragile
// across firmware revisions. It checks (a) no Step error, (b) the machine
// hasn't wandered out of ROM (catches stack-corruption / vector-table mis-
// routing), and (c) it actually moved forward past the reset vector.
func TestMachineBootMilestone_50(t *testing.T) {
	m := newMachine(t)
	const steps = 50
	const resetPC = uint32(0x00001B34) // Rev L reset PC
	for i := 0; i < steps; i++ {
		if err := m.CPU.Step(); err != nil {
			t.Fatalf("step %d (PC=%#06X): %v", i, m.CPU.Reg(cpu.PC), err)
		}
	}
	pc := m.CPU.Reg(cpu.PC)
	if pc == resetPC {
		t.Errorf("after %d steps PC still at reset vector %#06X — no forward progress", steps, pc)
	}
	if pc < 0x100 || pc >= machine.ROMSize {
		t.Errorf("after %d steps PC=%#06X is outside ROM body — likely fault", steps, pc)
	}
	t.Logf("Phase-1 gate PASS: %d boot instructions executed; PC=%#06X", steps, pc)
}

// TestMachineBootDeep — Run 1000 instructions from reset; the machine must not
// error or panic. This checks that the ROM/RAM/fault-path combination is stable
// deep into the boot sequence.
func TestMachineBootDeep(t *testing.T) {
	m := newMachine(t)
	for i := 0; i < 1000; i++ {
		if err := m.CPU.Step(); err != nil {
			t.Fatalf("step %d (PC=%#08X): %v", i, m.CPU.Reg(cpu.PC), err)
		}
	}
	finalPC := m.CPU.Reg(cpu.PC)
	t.Logf("1000 steps complete, final PC=%#08X", finalPC)
	if finalPC < 0x1000 {
		t.Errorf("final PC %#08X suspiciously low — machine may be stuck", finalPC)
	}
}

// TestMachineBootBulk — Phase-2 gate: use Run() to execute M68K cycles from
// reset, skipping known delay loops via LoopBreaker and injecting periodic
// IRQ5 ticks to drive the firmware timer. The machine must reach PC ≥ 0xB000
// (well into the main operating loop, past all boot stall points).
//
// Expected path (empirical, from boot-trace disassembly):
//
//	0x0B3E  reset vector
//	0x0B4A  SCI command write → SCI-ready loop unblocked by MMIO stub
//	0x1F5A  ROM checksum loop  — terminated by LoopBreaker(D0=0)
//	0x2160  March RAM test over 0xFEC000–0xFFC000 — passes (TestRAM mapped)
//	0x2420  Calibration delay  — terminated by LoopBreaker(D2=0)
//	0x36D66 Timer wait (bfca counter) — unblocked by periodic IRQ5 ticks
//	0x73C2  Display-controller init — unblocked by SCI status stub (bits 0,1,2)
//	0xF608  Sweep-ready wait (bit 12 of 0xFFF300) — unblocked by MMIO stub
//	0xB000+ Main operating loop — firmware is running
//
// Periodic IRQ5 injection: the IRQ5 handler at 0x19E2 increments RAM[0xFFBFCA]
// (the timer counter). Injecting IRQ5 every irqPeriod chunks drives the timer
// so that timer-wait loops exit normally. See also machine.go package comment.
func TestMachineBootBulk(t *testing.T) {
	m := newMachine(t)

	const totalCycles = 20_000_000
	const chunkCycles = 2000 // cycles per Run() call
	const breakThresh = 50   // consecutive same-loop chunks before forcing exit
	const targetPC = uint32(0xB000)

	// IRQ5 injection: fire a timer tick every irqPeriod chunks.
	// irqServiceCycles: enough cycles for the ~40-instruction IRQ5 handler.
	const irqPeriod = 5
	const irqServiceCycles = 400

	lb := emutest.NewLoopBreaker(breakThresh)

	for cyclesDone := 0; cyclesDone < totalCycles; cyclesDone += chunkCycles {
		m.CPU.Run(chunkCycles)
		pc := m.CPU.Reg(cpu.PC)
		lb.Check(pc, m.CPU.SetReg)

		// Periodic timer tick: assert IRQ5, let the handler run, deassert.
		chunk := cyclesDone / chunkCycles
		if chunk%irqPeriod == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Run(irqServiceCycles)
			m.CPU.SetIRQ(0)
		}

		if pc >= targetPC {
			t.Logf("Phase-2 gate PASS: reached PC=%#08X after ~%d cycles", pc, cyclesDone+chunkCycles)
			return
		}
	}

	finalPC := m.CPU.Reg(cpu.PC)
	t.Errorf("Phase-2 gate FAIL: only reached PC=%#08X after %d cycles (want ≥ %#08X)",
		finalPC, totalCycles, targetPC)
}

// TestCalNVRAMBootAccessPattern — Rev L regression test for what the firmware
// actually does with the cal NVRAM during the boot to operating loop. Measured
// via cmd/caltrace (faithful 100M-cycle boot): the firmware reads every byte of
// the 64 KB cal NVRAM exactly once (the byte-checksum sweep at ROM 0x454A) and
// re-reads offset 0 three more times for the CPU integrity test at ROM
// 0x44AA–0x44B8 (`move.l ($200000).l, D6; move.l D6, ($200000).l; cmp.l
// ($200000).l, D6`). Only offset 0 is ever written. No other offset is polled
// or compared against a constant — there is no boot-time "gate byte".
//
// This test pins those measurements so a future regression (e.g. a missed
// stall point that causes the firmware to re-enter the checksum, or a stray
// MMIO read that escapes into the cal region) is flagged immediately.
func TestCalNVRAMBootAccessPattern(t *testing.T) {
	t.Skip("Obsolete invariant — re-derive: with the A16 analog-bus model " +
		"(docs/ANALOG_BUS_MODEL.md) + M68K_EMULATE_ADDRESS_ERROR " +
		"(docs/DLP_STARTUP_DERAIL.md) the boot now runs the full startup DLP " +
		"and renders the operating UI, reading cal-NVRAM many times for cal/" +
		"display setup. The 'each offset read exactly once' pattern was an " +
		"artefact of the firmware freezing at the ROM checksum; re-derive the " +
		"new (richer) cal-NVRAM access pattern from the booted instrument.")
	m := newMachine(t)

	type counts struct{ reads, writes int }
	off0 := counts{}
	other := make(map[uint32]counts)
	totalReads := 0

	m.CalNVRAM.Trace = func(off uint32, sz bus.Size, val uint32, write bool) {
		_ = sz
		_ = val
		if off == 0 {
			if write {
				off0.writes++
			} else {
				off0.reads++
			}
			return
		}
		c := other[off]
		if write {
			c.writes++
		} else {
			c.reads++
			totalReads++
		}
		other[off] = c
	}

	// Faithful boot exercises the natural ROM checksum + march RAM test +
	// integrity test paths — the same paths a real instrument runs.
	m.BootToOperatingFaithful(100_000_000)

	// Integrity test at ROM 0x44AA-0x44B8: 2 reads + 1 write of long@0.
	// Plus the byte-checksum sweep reading byte 0 once: 1 more read at sz=1.
	// Total expected: 3 reads, 1 write at offset 0.
	if off0.reads < 2 || off0.reads > 4 {
		t.Errorf("offset 0 reads = %d, want 2..4 (integrity test + checksum sweep)", off0.reads)
	}
	if off0.writes != 1 {
		t.Errorf("offset 0 writes = %d, want exactly 1 (integrity test write-back)", off0.writes)
	}

	// Every other offset must be read exactly once and written zero times.
	for off, c := range other {
		if c.reads != 1 || c.writes != 0 {
			t.Errorf("offset %#06X has reads=%d writes=%d, want reads=1 writes=0 "+
				"(any re-read or write is a new firmware behaviour worth investigating)",
				off, c.reads, c.writes)
		}
	}

	// Coverage: the checksum sweep should touch the full 64 KB.
	const want = 65535 // offsets 1..65535 (offset 0 counted separately)
	if len(other) != want {
		t.Errorf("checksum sweep covered %d byte-offsets, want %d", len(other), want)
	}

	t.Logf("Rev L cal-NVRAM boot pattern OK: offset 0 = %d reads + %d writes; "+
		"%d other offsets each read exactly once",
		off0.reads, off0.writes, len(other))
}
