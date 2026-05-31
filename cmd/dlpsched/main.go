// dlpsched (phase 7): force 0xB0EC = 0x31 (spectrum mode) — the faithful upstream
// lever — and watch whether a9a0 goes positive, the operating loop is entered,
// and __GTTDRW fires. Tests whether display-mode is THE root lever for the trace.
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
	var op, gttdrw, sched6595A int
	var a9a0pos int
	m.Bus.OnRead = func(a uint32, s bus.Size, v uint32) {
		switch a {
		case 0x018568:
			op++
		case 0x065986:
			gttdrw++
		case 0x000D18:
			if m.CPU.Reg(cpu.A0) == 0x6595A {
				sched6595A++
			}
		}
	}
	m.BootToOperating(190_000_000)
	m.MMIO.SweepActive = true
	for i := 0; i < 6_000_000; i++ {
		if uint8(m.Bus.Read(0xFFB0EC, bus.Byte)) != 0x31 {
			m.Bus.Write(0xFFB0EC, bus.Byte, 0x31) // force spectrum mode
		}
		if int16(m.Bus.Read(0xFFA9A0, bus.Word)) >= 0 {
			a9a0pos++
		}
		if m.CPU.Step() != nil {
			break
		}
		if i%2000 == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Step()
			m.CPU.SetIRQ(0)
		}
		if i%1500 == 0 {
			m.CPU.SetIRQ(6)
			m.CPU.Step()
			m.CPU.SetIRQ(0)
		}
	}
	fmt.Printf("force B0EC=0x31: a9a0>=0 in %d/6M steps, op18568=%d, __GTTDRW=%d, sched6595A=%d, finalPC=%06X\n",
		a9a0pos, op, gttdrw, sched6595A, m.CPU.Reg(cpu.PC))
}
