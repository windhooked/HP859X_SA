// Package unicorn adapts the Unicorn engine's M68K core to cpu.CPU.
//
// It uses Unicorn's own flat 16 MB memory (loaded from the ROM image at reset),
// not the device bus, so it is intended as the differential oracle and the
// reverse-engineering core for pre-interrupt boot sequences. Unicorn (QEMU)
// has no clean IRQ-injection path, so the production machine routes memory
// through bus.Bus via the Musashi adapter instead.
//
// Running the resulting test binary requires libunicorn at load time; if it is
// not on the default search path, set DYLD_FALLBACK_LIBRARY_PATH (macOS) or
// LD_LIBRARY_PATH (Linux) to its directory.
package unicorn

import (
	"fmt"

	uc "github.com/unicorn-engine/unicorn/bindings/go/unicorn"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
)

// addrSpace is the 68000's 24-bit (16 MB) address range.
const addrSpace = 0x1000000

// CPU is a Unicorn-backed M68K core.
type CPU struct {
	mu uc.Unicorn
}

var _ cpu.CPU = (*CPU)(nil)

var regToUC = map[cpu.Reg]int{
	cpu.D0: uc.M68K_REG_D0, cpu.D1: uc.M68K_REG_D1, cpu.D2: uc.M68K_REG_D2, cpu.D3: uc.M68K_REG_D3,
	cpu.D4: uc.M68K_REG_D4, cpu.D5: uc.M68K_REG_D5, cpu.D6: uc.M68K_REG_D6, cpu.D7: uc.M68K_REG_D7,
	cpu.A0: uc.M68K_REG_A0, cpu.A1: uc.M68K_REG_A1, cpu.A2: uc.M68K_REG_A2, cpu.A3: uc.M68K_REG_A3,
	cpu.A4: uc.M68K_REG_A4, cpu.A5: uc.M68K_REG_A5, cpu.A6: uc.M68K_REG_A6, cpu.A7: uc.M68K_REG_A7,
	cpu.PC: uc.M68K_REG_PC, cpu.SR: uc.M68K_REG_SR,
}

// New creates a core with image mapped at address 0 over a flat 16 MB space.
func New(image []byte) (*CPU, error) {
	if len(image) > addrSpace {
		return nil, fmt.Errorf("unicorn: image %d bytes exceeds 24-bit space", len(image))
	}
	mu, err := uc.NewUnicorn(uc.ARCH_M68K, uc.MODE_BIG_ENDIAN)
	if err != nil {
		return nil, err
	}
	if err := mu.MemMap(0, addrSpace); err != nil {
		return nil, err
	}
	if err := mu.MemWrite(0, image); err != nil {
		return nil, err
	}
	return &CPU{mu: mu}, nil
}

func (c *CPU) read32(addr uint32) uint32 {
	b, err := c.mu.MemRead(uint64(addr), 4)
	if err != nil {
		panic(err)
	}
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

// Reset loads SP and PC from the reset vector, then normalises SR to the
// canonical 68000 post-reset value (0x2700: supervisor mode, interrupt
// mask=7, CCR=0). Unicorn zero-initialises SR so the normalisation is
// required for DiffCores comparisons against the Musashi adapter.
//
// SR must be set before A7: Unicorn tracks a separate user/supervisor A7;
// writing A7 while SR=0 (user mode) would update the user stack pointer,
// leaving the supervisor stack pointer zero after the mode switch.
func (c *CPU) Reset() {
	c.SetReg(cpu.SR, 0x2700)
	c.SetReg(cpu.A7, c.read32(0))
	c.SetReg(cpu.PC, c.read32(4))
}

// Step executes a single instruction starting at the current PC.
func (c *CPU) Step() error {
	pc, err := c.mu.RegRead(uc.M68K_REG_PC)
	if err != nil {
		return err
	}
	return c.mu.StartWithOptions(pc, 0, &uc.UcOptions{Count: 1})
}

func (c *CPU) Reg(r cpu.Reg) uint32 {
	v, err := c.mu.RegRead(regToUC[r])
	if err != nil {
		panic(err)
	}
	return uint32(v)
}

func (c *CPU) SetReg(r cpu.Reg, v uint32) {
	if err := c.mu.RegWrite(regToUC[r], uint64(v)); err != nil {
		panic(err)
	}
}

// SetIRQ is a no-op: Unicorn's M68K core exposes no IRQ-injection path. The
// interrupt model lives in the Musashi adapter; this core is only used for
// pre-interrupt boot sequences and differential comparison.
func (c *CPU) SetIRQ(level int) { _ = level }
