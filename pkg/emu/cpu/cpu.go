// Package cpu defines the core-agnostic M68K interface that the emulator's
// bus, devices, and test framework target. Concrete cores live in subpackages:
// unicorn (the differential oracle / reverse-engineering core) today, and
// musashi (the production core, routing memory through bus.Bus) next.
package cpu

import "github.com/windhooked/HP859X_SA/pkg/emu/bus"

// Reg identifies a M68K register that every core exposes.
type Reg int

const (
	D0 Reg = iota
	D1
	D2
	D3
	D4
	D5
	D6
	D7
	A0
	A1
	A2
	A3
	A4
	A5
	A6
	A7 // active stack pointer
	PC
	SR
)

// String renders a register name for trace/diff output.
func (r Reg) String() string {
	switch {
	case r >= D0 && r <= D7:
		return "D" + string(rune('0'+int(r-D0)))
	case r >= A0 && r <= A7:
		return "A" + string(rune('0'+int(r-A0)))
	case r == PC:
		return "PC"
	case r == SR:
		return "SR"
	default:
		return "?"
	}
}

// All lists every register in a stable order, for trace capture and diffing.
var All = []Reg{D0, D1, D2, D3, D4, D5, D6, D7, A0, A1, A2, A3, A4, A5, A6, A7, PC, SR}

// Memory is the address space a core reads and writes. *bus.Bus satisfies it.
type Memory interface {
	Read(addr uint32, sz bus.Size) uint32
	Write(addr uint32, sz bus.Size, val uint32)
}

// CPU is a single M68K core. Cores come up halted; call Reset to load the
// initial SP and PC from the reset vector before stepping.
type CPU interface {
	// Reset loads SP from the long at address 0 and PC from the long at 4.
	Reset()
	// Step executes a single instruction.
	Step() error
	// Reg reads a register's current value.
	Reg(r Reg) uint32
	// SetReg overwrites a register.
	SetReg(r Reg, v uint32)
	// SetIRQ asserts interrupt priority level 1-7 (0 clears it). Some cores
	// do not support injection; see the adapter's documentation.
	SetIRQ(level int)
}
