// Command looptrace boots to steady state and answers, for the trace-draw
// blocker: (a) does PC ever enter the trace-process (0x20A40) or sweep-done
// (0x4E730) regions? (b) does $b0a0 bit11 (the trace-draw busy gate) ever
// clear? (c) what is the exact innermost spin PC? It drives the sweep the
// hardware way (IRQ1 step + IRQ6 capture) so acquisition progresses.
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
	rdL := func(a uint32) uint32 { return m.Bus.Read(a, bus.Long) }
	for done := 0; done < 60_000_000; done += 2000 {
		m.CPU.Run(2000)
		lb.Check(m.CPU.Reg(cpu.PC), m.CPU.SetReg)
		if (done/2000)%5 == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Run(400)
			m.CPU.SetIRQ(0)
		}
	}

	pcHist := map[uint32]int{}
	keyHist := map[uint32]int{}    // DLP search key (d0) at 0x32B7C
	callerHist := map[uint32]int{} // caller of the search (return addr)
	var stackSample []uint32       // A6-chain call stack captured once
	const chunks = 6000
	for i := 0; i < chunks; i++ {
		for s := 0; s < 300; s++ {
			if m.CPU.Step() != nil {
				break
			}
			pc := m.CPU.Reg(cpu.PC)
			pcHist[pc]++
			if pc == 0x32B7C { // DLP record-search: key=d0, caller=ret addr
				keyHist[m.CPU.Reg(cpu.D0)&0xFFFF]++
				callerHist[m.Bus.Read(m.CPU.Reg(cpu.A6)+4, bus.Long)]++
				if len(stackSample) == 0 { // capture A6-chain call stack once
					a6 := m.CPU.Reg(cpu.A6)
					for depth := 0; depth < 18 && a6 >= 0xFF0000 && a6 < 0xFFFFFE; depth++ {
						ret := m.Bus.Read(a6+4, bus.Long)
						stackSample = append(stackSample, ret)
						a6 = m.Bus.Read(a6, bus.Long)
					}
				}
			}
		}
		if i%5 == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Run(400)
			m.CPU.SetIRQ(0)
		}
		// Drive a full sweep (IRQ1 step + IRQ6 capture).
		for n := 0; n < 450 && m.CPU.Reg(cpu.A5) < rdL(0xFFBF30); n++ {
			m.CPU.SetIRQ(1)
			m.CPU.Run(250)
			m.CPU.SetIRQ(0)
			m.Bus.Write(0xFFF200, bus.Word, uint32(0x0140+(n%200)))
			m.CPU.SetIRQ(6)
			m.CPU.Run(250)
			m.CPU.SetIRQ(0)
		}
	}
	type kv struct {
		k uint32
		n int
	}
	topN := func(h map[uint32]int, label string, lim int) {
		var s []kv
		for k, n := range h {
			s = append(s, kv{k, n})
		}
		sort.Slice(s, func(i, j int) bool { return s[i].n > s[j].n })
		fmt.Println(label)
		for i, e := range s {
			if i >= lim {
				break
			}
			d, _ := m.CPU.Disasm(e.k)
			fmt.Printf("  %06X x%-7d %s\n", e.k, e.n, d)
		}
	}
	fmt.Printf("DLP record-search (0x32B70) call count: %d\n", func() int {
		t := 0
		for _, n := range keyHist {
			t += n
		}
		return t
	}())
	fmt.Println("call stack (A6 chain) at the DLP search:")
	for _, r := range stackSample {
		d, _ := m.CPU.Disasm(r)
		fmt.Printf("  %06X  %s\n", r, d)
	}
	topN(keyHist, "search keys (d0):", 12)
	topN(callerHist, "search callers (return addr):", 12)
	type pe struct {
		p uint32
		n int
	}
	var pes []pe
	for p, n := range pcHist {
		pes = append(pes, pe{p, n})
	}
	sort.Slice(pes, func(i, j int) bool { return pes[i].n > pes[j].n })
	fmt.Println("top 20 exact PCs (the innermost spin):")
	for i, e := range pes {
		if i >= 20 {
			break
		}
		d, _ := m.CPU.Disasm(e.p)
		fmt.Printf("  %06X x%-7d %s\n", e.p, e.n, d)
	}
}
