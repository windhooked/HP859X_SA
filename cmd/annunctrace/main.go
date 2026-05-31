// Command annunctrace cracks the annunciator status word(s). The drawer reads a
// RAM status word, tests a bit (btst), and if set fetches the annunciator string
// from ROM (string base 0x2b31e + offset). By hooking bus reads we capture, for
// each annunciator-string fetch in ROM 0x2b360..0x2b480, the recent RAM-word
// reads — the status word + bit the firmware tested. Boots to the live UI then
// runs a window so the drawer refreshes.
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

	// Capture the PC of every instruction that READS the annunciator metadata
	// table (ROM 0x26050..0x26200) — that is the drawer's table-walk loop. The
	// PC histogram localises the drawer so we can disassemble it and find the
	// global status word it tests.
	pcHist := map[uint32]int{}
	m.Bus.OnRead = func(addr uint32, sz bus.Size, val uint32) {
		if addr >= 0x26050 && addr <= 0x26200 {
			pcHist[m.CPU.Reg(cpu.PC)]++
		}
	}
	for done := 0; done < 165_000_000; done += 2000 {
		m.CPU.Run(2000)
		lb.Check(m.CPU.Reg(cpu.PC), m.CPU.SetReg)
		if (done/2000)%5 == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Run(400)
			m.CPU.SetIRQ(0)
		}
	}
	m.Bus.OnRead = nil

	type pc struct {
		p uint32
		n int
	}
	var pcs []pc
	for p, n := range pcHist {
		pcs = append(pcs, pc{p, n})
	}
	sort.Slice(pcs, func(i, j int) bool { return pcs[i].n > pcs[j].n })
	fmt.Printf("PCs reading the annunciator metadata table (0x26050..0x26200) — the drawer:\n")
	for i, e := range pcs {
		if i >= 16 {
			break
		}
		d, _ := m.CPU.Disasm(e.p)
		fmt.Printf("  %06X x%-6d %s\n", e.p, e.n, d)
	}
}
