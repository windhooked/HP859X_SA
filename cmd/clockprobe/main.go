// clockprobe characterizes the 8593's software real-time clock during boot.
// There is NO hardware RTC chip — time is kept by the IRQ5 handler (ROM 0x3ECE),
// which bumps tick counters 0xBF12 (deadline-timer base used by fcn.47fc/4824 ADC
// polls) and 0xBF16, and decrements settle timers 0xBF1A/0xBF22. It reports their
// values + the SR interrupt-mask level so the clock's advance rate and the early
// IRQ5-masking are both visible. See the RTC survey in docs/rom_annotations.md.
package main

import (
	"fmt"

	"github.com/windhooked/HP859X_SA/internal/emutest"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
	"github.com/windhooked/HP859X_SA/pkg/emu/machine"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

func main() {
	rom, _ := romloader.LoadDir("hp8593a_eeproms")
	m, _ := machine.New8593A(rom)
	m.CPU.Reset()
	lb := emutest.NewLoopBreaker(50)

	rd := func(a uint32) uint32 { return uint32(m.Bus.Read(a, 4)) }
	maskedChunks := 0
	fmt.Printf("%-12s %-10s %-10s %-12s %-12s %s\n", "cycles", "BF12", "BF16", "BF1A", "BF22", "IPL")
	report := func(done int) {
		fmt.Printf("%-12d %#-10x %#-10x %#-12x %#-12x %d\n", done,
			rd(0xFFBF12), rd(0xFFBF16), rd(0xFFBF1A), rd(0xFFBF22), (m.CPU.Reg(cpu.SR)>>8)&7)
	}
	const total = 120_000_000
	next := 0
	for done := 0; done < total; done += 2000 {
		m.CPU.Run(2000)
		if (m.CPU.Reg(cpu.SR)>>8)&7 >= 5 {
			maskedChunks++
		}
		lb.Check(m.CPU.Reg(cpu.PC), m.CPU.SetReg)
		if (done/2000)%5 == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Run(400)
			m.CPU.SetIRQ(0)
		}
		if done >= next {
			report(done)
			next += 20_000_000
		}
	}
	report(total)
	fmt.Printf("chunks with IRQ5 masked (IPL>=5): %d / %d\n", maskedChunks, total/2000)
}
