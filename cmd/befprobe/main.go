// Command befprobe finds the writers of the annunciator source-status flags
// (0xFFB1E0/B1F0/B1F6/B1F8/B1FA) that fcn.11B9A aggregates into the annunciator
// display words — i.e. the hardware-status checks behind REF UNLOCK / ADC-TIME
// FAIL / OVEN COLD. Reports each flag's boot value + writer PCs.
package main

import (
	"fmt"
	"sort"

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
	flags := map[uint32]string{0xFFB1E0: "B1E0", 0xFFB1F0: "B1F0", 0xFFB1F6: "B1F6", 0xFFB1F8: "B1F8", 0xFFB1FA: "B1FA"}
	writers := map[uint32]map[uint32]uint32{} // flag -> {PC -> lastVal}
	for f := range flags {
		writers[f] = map[uint32]uint32{}
	}
	m.Bus.OnWrite = func(a uint32, sz bus.Size, v uint32) {
		fa := a &^ 1
		if _, ok := flags[fa]; ok {
			writers[fa][m.CPU.Reg(cpu.PC)] = v & 0xFFFF
		}
	}
	for c := 0; c < 165_000_000; c += 2000 {
		m.CPU.Run(2000)
		lb.Check(m.CPU.Reg(cpu.PC), m.CPU.SetReg)
		if (c/2000)%5 == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Run(400)
			m.CPU.SetIRQ(0)
		}
	}
	var fs []uint32
	for f := range flags {
		fs = append(fs, f)
	}
	sort.Slice(fs, func(i, j int) bool { return fs[i] < fs[j] })
	for _, f := range fs {
		fmt.Printf("\n%s (%06X) = %04X after boot; writers:\n", flags[f], f, uint16(m.Bus.Read(f, bus.Word)))
		for pc, v := range writers[f] {
			d, _ := m.CPU.Disasm(pc)
			fmt.Printf("    PC %06X val=%04X  %s\n", pc, v, d)
		}
	}
}
