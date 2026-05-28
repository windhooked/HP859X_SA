// Command sweepprobe boots the 8593A and dumps the sweep-acquisition state so we
// can see whether the firmware has armed a sweep (IRQ6 dispatch handler bfea,
// trace-buffer end bfe6, detector mode, DAC mirrors). Read-only — no interrupt
// injection — so it is safe to run before we know the buffer is valid.
package main

import (
	"fmt"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
	"github.com/windhooked/HP859X_SA/pkg/emu/machine"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

func main() {
	rom, _ := romloader.LoadDir("hp8593a_eeproms")
	m, _ := machine.New8593A(rom)
	m.CPU.Reset()
	m.BootToOperatingFaithful(40_000_000)

	rd := func(addr uint32, sz bus.Size) uint32 { return m.Bus.Read(addr, sz) }
	fmt.Printf("final PC=%06X\n", m.CPU.Reg(cpu.PC))
	fmt.Println("--- sweep-acquisition RAM (short .w addrs sign-extend to 0xFFxxxx) ---")
	fmt.Printf("  bfea (IRQ6 dispatch handler) = %08X\n", rd(0xFFBFEA, bus.Long))
	fmt.Printf("  bfe6 (trace-buffer end)      = %08X\n", rd(0xFFBFE6, bus.Long))
	fmt.Printf("  bfee                         = %08X\n", rd(0xFFBFEE, bus.Long))
	fmt.Printf("  bff2 (decim reload)          = %04X\n", rd(0xFFBFF2, bus.Word))
	fmt.Printf("  bff4 (decim counter)         = %04X\n", rd(0xFFBFF4, bus.Word))
	fmt.Printf("  bff8                         = %04X\n", rd(0xFFBFF8, bus.Word))
	fmt.Printf("  98d8 (sweep DAC mirror)      = %04X\n", rd(0xFF98D8, bus.Word))
	fmt.Printf("  98f8 (sweep status mirror)   = %04X\n", rd(0xFF98F8, bus.Word))
	fmt.Printf("  9744                         = %04X\n", rd(0xFF9744, bus.Word))
	fmt.Println("--- CPU sweep pointer ---")
	fmt.Printf("  A5 (sample dest)             = %08X\n", m.CPU.Reg(cpu.A5))
	fmt.Printf("  SR                           = %08X (IPL=%d)\n",
		m.CPU.Reg(cpu.SR), (m.CPU.Reg(cpu.SR)>>8)&7)

	// Is bfea a plausible IRQ6 handler (one of the known sweep capture modes)?
	h := rd(0xFFBFEA, bus.Long)
	known := map[uint32]string{0x1B5E: "sample", 0x1BB2: "pos-peak", 0x1BD6: "sample2", 0x1BF2: "neg-peak", 0x1C18: "mode5"}
	if name, ok := known[h]; ok {
		fmt.Printf("=> sweep ARMED: bfea -> %06X (%s detection)\n", h, name)
	} else {
		fmt.Printf("=> bfea=%06X not a known capture handler (sweep may not be armed)\n", h)
	}
}
