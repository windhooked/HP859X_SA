//go:build ignore
// +build ignore

// disasm_test.go — manual diagnostic test (not run by default).
// Run with:
//
//	go test -v -run TestDisasmStallRegion ./pkg/emu/machine/
//
// Loads the ROM from the *top*.bin source chips, sets up a machine, and
// disassembles ±64 instructions around the stall PC 0x21DA to identify the
// next polling loop that needs a device stub or skip rule.
package machine_test

import (
	"fmt"
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
)

func TestDisasmStallRegion(t *testing.T) {
	m := newMachine(t)

	// Disassemble a window of ROM around the observed stall PC.
	const stallPC = uint32(0x21DA)
	const window = 64 // instructions before/after

	// Walk backward: find startPC by stepping backward window instructions.
	// Since instructions are variable length, walk from ROM base forward until
	// we're within range, then collect.
	type insn struct {
		pc   uint32
		text string
		sz   uint32
	}

	// Collect instructions from stallPC-256 forward for ~512 bytes.
	from := stallPC - 256
	var insns []insn
	for pc := from; pc < stallPC+256; {
		text, sz := m.CPU.Disasm(pc)
		if sz == 0 {
			sz = 2 // skip invalid word
		}
		insns = append(insns, insn{pc, text, sz})
		pc += sz
	}

	// Print, marking the stall PC.
	for _, in := range insns {
		marker := "   "
		if in.pc == stallPC {
			marker = ">>>"
		}
		fmt.Printf("%s  %08X  %s\n", marker, in.pc, in.text)
	}

	// Also dump register state of a machine that ran until stall.
	const chunkCycles  = 2000
	const stallChunks  = 100
	totalCycles := 2_000_000

	pageCounts := make(map[uint32]*int)
	for cyclesDone := 0; cyclesDone < totalCycles; cyclesDone += chunkCycles {
		m.CPU.Run(chunkCycles)
		pc := m.CPU.Reg(cpu.PC)
		skipLoops(m.CPU, pc, pageCounts, stallChunks)
	}

	regs := []struct {
		name string
		r    cpu.Reg
	}{
		{"D0", cpu.D0}, {"D1", cpu.D1}, {"D2", cpu.D2}, {"D3", cpu.D3},
		{"D4", cpu.D4}, {"D5", cpu.D5}, {"D6", cpu.D6}, {"D7", cpu.D7},
		{"A0", cpu.A0}, {"A1", cpu.A1}, {"A2", cpu.A2}, {"A3", cpu.A3},
		{"A4", cpu.A4}, {"A5", cpu.A5}, {"A6", cpu.A6}, {"A7", cpu.A7},
		{"PC", cpu.PC}, {"SR", cpu.SR},
	}
	fmt.Printf("\n=== Register state at stall ===\n")
	for _, r := range regs {
		fmt.Printf("  %-2s = %08X\n", r.name, m.CPU.Reg(r.r))
	}

	// Disassemble around actual stall PC.
	actualPC := m.CPU.Reg(cpu.PC)
	fmt.Printf("\n=== Disasm around stall PC=%08X ===\n", actualPC)
	from = actualPC - 32
	for pc := from; pc < actualPC+64; {
		text, sz := m.CPU.Disasm(pc)
		if sz == 0 {
			sz = 2
		}
		marker := "   "
		if pc == actualPC {
			marker = ">>>"
		}
		fmt.Printf("%s  %08X  %s\n", marker, pc, text)
		pc += sz
	}
}
