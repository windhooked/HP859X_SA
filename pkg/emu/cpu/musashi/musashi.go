// Package musashi adapts the Musashi M68K C core to the cpu.CPU interface.
//
// Memory accesses are routed through a bus.Bus provided at construction time;
// the bus dispatches ROM, RAM, and (Phase 2+) MMIO device accesses.
// The active bus is held in a package-level variable (activeBus, see
// bus_callbacks.go) because Musashi is a C-level singleton — only one instance
// may be active at a time.
package musashi

/*
#cgo CFLAGS: -I${SRCDIR}/../../../../third_party/musashi
#include "m68k.h"
#include "bridge.h"
*/
import "C"
import (
	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
)

// regToMusashi maps cpu.Reg to Musashi's m68k_register_t enum. The enum values
// happen to be identical for D0-D7, A0-A7, PC, SR (all = 0..17).
var regToMusashi = [...]C.m68k_register_t{
	cpu.D0: C.M68K_REG_D0, cpu.D1: C.M68K_REG_D1,
	cpu.D2: C.M68K_REG_D2, cpu.D3: C.M68K_REG_D3,
	cpu.D4: C.M68K_REG_D4, cpu.D5: C.M68K_REG_D5,
	cpu.D6: C.M68K_REG_D6, cpu.D7: C.M68K_REG_D7,
	cpu.A0: C.M68K_REG_A0, cpu.A1: C.M68K_REG_A1,
	cpu.A2: C.M68K_REG_A2, cpu.A3: C.M68K_REG_A3,
	cpu.A4: C.M68K_REG_A4, cpu.A5: C.M68K_REG_A5,
	cpu.A6: C.M68K_REG_A6, cpu.A7: C.M68K_REG_A7,
	cpu.PC: C.M68K_REG_PC, cpu.SR: C.M68K_REG_SR,
}

// CPU is a Musashi-backed M68K core. There can only be one active Musashi
// instance at a time (global C state); callers must not create more than one.
type CPU struct{}

var _ cpu.CPU = (*CPU)(nil)

// New creates a CPU backed by b. All memory accesses are routed through b;
// the caller is responsible for mapping ROM, RAM, and any MMIO devices onto
// b before stepping the CPU.
func New(b *bus.Bus) (*CPU, error) {
	activeBus = b
	C.musashi_init()
	return &CPU{}, nil
}

// Reset pulses Musashi's RESET line (which loads SP/PC from the reset vector),
// drains the reset-exception cycle penalty, then normalises SR to the canonical
// 68000 post-reset value (0x2700: supervisor mode, interrupt mask=7, CCR=0).
//
// Without the drain, the first m68k_execute call after reset eats the 40-cycle
// penalty without executing any instructions. Without the SR normalisation,
// Musashi's zero-initialised not_z_flag would leave the Z CCR bit set, causing
// spurious divergence in DiffCores comparisons.
func (c *CPU) Reset() {
	C.m68k_pulse_reset()
	C.musashi_drain_reset()
	C.m68k_set_reg(C.M68K_REG_SR, 0x2700) // supervisor, IPL=7, CCR=0
}

// Step executes exactly one instruction.
func (c *CPU) Step() error {
	C.musashi_step(1)
	return nil
}

// Run executes for (at least) cycles M68K bus cycles. Musashi may overshoot
// by up to one instruction's cycle count. The actual cycle count executed is
// returned. Use for bulk execution (e.g. skipping busy-wait delay loops)
// without paying the cgo-crossing cost of thousands of Step() calls.
func (c *CPU) Run(cycles int) int {
	return int(C.musashi_run(C.int(cycles)))
}

// Reg returns the value of register r.
func (c *CPU) Reg(r cpu.Reg) uint32 {
	return uint32(C.m68k_get_reg(nil, regToMusashi[r]))
}

// SetReg overwrites register r with v.
func (c *CPU) SetReg(r cpu.Reg, v uint32) {
	C.m68k_set_reg(regToMusashi[r], C.uint(v))
}

// SetIRQ asserts interrupt priority level (1–7; 0 clears). The HP 8593A uses
// autovectored interrupts; the Musashi autovector default applies here.
func (c *CPU) SetIRQ(level int) {
	C.m68k_set_irq(C.uint(level))
}

// Disasm disassembles one M68K instruction at addr using the active bus for
// memory reads. Returns the mnemonic string and the byte-size of the
// instruction so callers can advance addr.
func (c *CPU) Disasm(addr uint32) (string, uint32) {
	var buf [80]C.char
	sz := C.musashi_disasm(C.uint(addr), &buf[0], C.uint(len(buf)))
	return C.GoString(&buf[0]), uint32(sz)
}
