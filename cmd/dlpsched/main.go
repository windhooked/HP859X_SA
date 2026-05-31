// dlpsched (phase 4): hypothesis test — force 0xA9A0>=0 (the stuck-loop gate)
// and watch whether the firmware advances: operating loop fcn.18568 entered?
// trace-draw __GTTDRW (0x65986) reached / source 0x6595A scheduled?
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

	var op18568, gttdrw, sched6595A, interp34EE8 int
	m.Bus.OnRead = func(a uint32, s bus.Size, v uint32) {
		switch a {
		case 0x018568:
			op18568++
		case 0x065986:
			gttdrw++
		case 0x034EE8:
			interp34EE8++
		case 0x000D18:
			if m.CPU.Reg(cpu.A0) == 0x6595A {
				sched6595A++
			}
		}
	}
	m.BootToOperating(190_000_000)
	fmt.Printf("before force: op18568=%d interp=%d gttdrw=%d sched6595A=%d\n", op18568, interp34EE8, gttdrw, sched6595A)

	// Now step with 0xA9A0 forced non-negative whenever it goes negative.
	op18568, gttdrw, sched6595A, interp34EE8 = 0, 0, 0, 0
	m.MMIO.SweepActive = true
	for i := 0; i < 5_000_000; i++ {
		if int16(m.Bus.Read(0xFFA9A0, bus.Word)) < 0 {
			m.Bus.Write(0xFFA9A0, bus.Word, 0x0080) // force a sane mid-sweep point count
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
			m.CPU.SetIRQ(6) // drive sweep capture
			m.CPU.Step()
			m.CPU.SetIRQ(0)
		}
	}
	fmt.Printf("after force:  op18568=%d interp=%d gttdrw=%d sched6595A=%d  finalPC=%06X\n",
		op18568, interp34EE8, gttdrw, sched6595A, m.CPU.Reg(cpu.PC))
}
