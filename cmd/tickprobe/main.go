// Command tickprobe — try adding b1e4=0x34 as the missing pre-arm
// and see if 1B cycles reaches the bclr at 0x18F42.
package main

import (
	"fmt"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
	"github.com/windhooked/HP859X_SA/pkg/emu/machine"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

func main() {
	img, _ := romloader.LoadDir("hp8593a_eeproms")
	m, _ := machine.New8593A(img)
	m.CPU.Reset()
	m.BootToOperating(30_000_000)

	m.CPU.SetIRQ(3)
	m.CPU.Run(400)
	m.CPU.SetIRQ(0)

	m.Bus.Write(0xFFB1E0, bus.Word, 0x0200)
	m.Bus.Write(0xFFBEFA, bus.Word, m.Bus.Read(0xFFBEFA, bus.Word)&^0x0400)
	m.Bus.Write(0xFF9AFB, bus.Byte, m.Bus.Read(0xFF9AFB, bus.Byte)|0x04)
	m.Bus.Write(0xFFBEFA, bus.Word, m.Bus.Read(0xFFBEFA, bus.Word)|(1<<13))

	// NEW: set b1e4 = 0x34. fcn.568F6 + fcn.11750 + others use this
	// as the "fast return" path indicator.
	m.Bus.Write(0xFFB1E4, bus.Word, 0x0034)
	fmt.Println("b1e4 forced to 0x0034")

	m.CPU.SetReg(cpu.PC, machine.OperatingTickDeepBlock)

	const sliceCycles = 5_000_000
	const totalSlices = 100 // = 500M cycles
	for slice := 0; slice < totalSlices; slice++ {
		m.CPU.SetIRQ(5)
		m.CPU.Run(400)
		m.CPU.SetIRQ(0)
		m.CPU.Run(sliceCycles)

		bc67 := byte(m.Bus.Read(0xFFBC67, bus.Byte))
		befa := m.Bus.Read(0xFFBEFA, bus.Word)
		pc := m.CPU.Reg(cpu.PC)
		bc26 := m.Bus.Read(0xFFBC26, bus.Word)
		if bc67&0x01 == 0 {
			fmt.Printf("\n*** bc67 CLEARED at slice %d (%dM cycles) PC=%#06x ***\n",
				slice, (slice+1)*5, pc)
			fmt.Printf("    befa bit 13: %v, bc26: %#04x\n",
				befa&0x2000 != 0, bc26)
			return
		}
		if slice%10 == 0 {
			fmt.Printf("  slice %3d (%4dM)  PC=%#06x  bc67=%#02x  befa&2000=%v  bc26=%#04x\n",
				slice, (slice+1)*5, pc, bc67, befa&0x2000 != 0, bc26)
		}
	}
	fmt.Printf("\nbc67 never cleared.\n")
}
