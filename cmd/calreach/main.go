// calreach checks whether the ADC auto-cal sweep (entry 0x5ED7E, dispatched via
// fcn.b68 from fcn.48316) runs during boot, and whether it writes the cal-valid
// signature 0xd2d2 to 0xFF94E4. See docs/POST_SELFTEST.md "CRACKED (2026-06-01)".
package main

import (
	"fmt"

	"github.com/windhooked/HP859X_SA/internal/emutest"
	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
	"github.com/windhooked/HP859X_SA/pkg/emu/machine"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

func main() {
	rom, _ := romloader.LoadDir("hp8593a_eeproms")
	m, _ := machine.New8593A(rom)
	m.CPU.Reset()
	lb := emutest.NewLoopBreaker(50)

	calEntry, sigWrites, e4writes := 0, 0, 0
	m.Bus.OnWrite = func(addr uint32, sz bus.Size, val uint32) {
		if addr == 0xFF94E4 {
			e4writes++
			if val&0xFFFF == 0xd2d2 {
				sigWrites++
			}
		}
	}
	const total = 200_000_000
	for done := 0; done < total; done += 2000 {
		_, hit := m.CPU.RunUntil(2000, 0x5ED7E)
		if hit {
			calEntry++
		}
		lb.Check(m.CPU.Reg(cpu.PC), m.CPU.SetReg)
		if (done/2000)%5 == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Run(400)
			m.CPU.SetIRQ(0)
		}
	}
	fmt.Printf("cal-sweep entry 0x5ED7E reached: %d times\n", calEntry)
	fmt.Printf("0xFF94E4 writes total:           %d\n", e4writes)
	fmt.Printf("0xFF94E4 <- 0xd2d2 (cal valid):  %d\n", sigWrites)
}
