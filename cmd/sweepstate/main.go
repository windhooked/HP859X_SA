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
	m.BootToOperating(250_000_000)
	rd := func(a uint32) uint32 { return uint32(m.Bus.Read(a, bus.Word)) }
	fmt.Println("steady-state sweep cells, sampled every 2M cycles:")
	for k := 0; k < 6; k++ {
		fmt.Printf("  a9a0=%04x a9a4=%04x a2e8=%04x a2ee=%04x befa=%04x f300=%04x bf34=%04x\n",
			rd(0xFFA9A0), rd(0xFFA9A4), rd(0xFFA2E8), rd(0xFFA2EE), rd(0xFFBEFA), rd(0xFFF300), rd(0xFFBF34))
		m.CPU.Run(2_000_000)
	}
	fmt.Printf("PC=%06X\n", m.CPU.Reg(cpu.PC))
}
