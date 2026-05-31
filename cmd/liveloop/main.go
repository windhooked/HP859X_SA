package main

import (
	"fmt"
	"sort"

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
	for done := 0; done < 160_000_000; done += 2000 {
		m.CPU.Run(2000)
		lb.Check(m.CPU.Reg(cpu.PC), m.CPU.SetReg)
		if (done/2000)%5 == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Run(400)
			m.CPU.SetIRQ(0)
		}
	}
	h := map[uint32]int{}
	op := false
	for i := 0; i < 3_000_000; i++ {
		pc := m.CPU.Reg(cpu.PC)
		h[pc]++
		if pc >= 0x18560 && pc < 0x18B00 {
			op = true
		}
		if m.CPU.Step() != nil {
			break
		}
		if i%2500 == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Step()
			m.CPU.SetIRQ(0)
		}
	}
	type kv struct {
		p uint32
		n int
	}
	var s []kv
	for p, n := range h {
		s = append(s, kv{p, n})
	}
	sort.Slice(s, func(i, j int) bool { return s[i].n > s[j].n })
	fmt.Printf("operating loop fcn.18568 reached: %v\n", op)
	fmt.Println("top 24 live-state PCs:")
	for i, e := range s {
		if i >= 24 {
			break
		}
		d, _ := m.CPU.Disasm(e.p)
		fmt.Printf("  %06X x%-7d %s\n", e.p, e.n, d)
	}
}
