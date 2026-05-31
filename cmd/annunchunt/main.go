// Command annunchunt: record each annunciator's RAM string-copy address, then
// collect every distinct PC that reads that copy over the whole boot. The
// glyph-draw PC appears for SHOWN annunciators (REF-UNLOCK/ADC-TIME/OVEN) but
// not hidden ones (ADC-GND/ADC-2V) — that difference is the status-gated draw.
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

	names := map[uint32]string{0x2b37f: "ADC-TIME(shown)", 0x2b38b: "ADC-GND(hidden)", 0x2b39b: "ADC-2V(hidden)", 0x2b3a7: "OVEN(shown)", 0x2b3fd: "REF-UNLOCK(shown)"}
	destName := map[uint32]string{}
	copied := map[uint32]bool{}
	readers := map[string]map[uint32]int{} // name -> {PC -> count}

	m.Bus.OnRead = func(addr uint32, sz bus.Size, val uint32) {
		pc := m.CPU.Reg(cpu.PC)
		if pc == 0x6A48 {
			for base := range names {
				if addr >= base && addr <= base+1 && !copied[base] {
					copied[base] = true
					destName[m.CPU.Reg(cpu.A0)&^1] = names[base]
				}
			}
			return
		}
		for d, nm := range destName {
			if addr >= d && addr <= d+10 {
				if readers[nm] == nil {
					readers[nm] = map[uint32]int{}
				}
				readers[nm][pc]++
			}
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
	for _, nm := range []string{"REF-UNLOCK(shown)", "ADC-TIME(shown)", "OVEN(shown)", "ADC-GND(hidden)", "ADC-2V(hidden)"} {
		fmt.Printf("\n%s reader PCs:\n", nm)
		var pcs []uint32
		for pc := range readers[nm] {
			pcs = append(pcs, pc)
		}
		sort.Slice(pcs, func(i, j int) bool { return pcs[i] < pcs[j] })
		for _, pc := range pcs {
			d, _ := m.CPU.Disasm(pc)
			fmt.Printf("  %06X x%-4d %s\n", pc, readers[nm][pc], d)
		}
	}
}
