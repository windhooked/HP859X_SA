// Command looptrace boots to the post-boot steady state and dumps a contiguous
// instruction trace so the actual loop the firmware is stuck in can be read
// directly (rather than inferred from a PC histogram). It prints the distinct
// PCs in execution order with disassembly, and flags the loop period.
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
	for done := 0; done < 60_000_000; done += 2000 {
		m.CPU.Run(2000)
		lb.Check(m.CPU.Reg(cpu.PC), m.CPU.SetReg)
		if (done/2000)%5 == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Run(400)
			m.CPU.SetIRQ(0)
		}
	}
	// Settle a touch more.
	m.CPU.Run(200000)

	// Record a long PC sequence.
	const N = 12000
	seq := make([]uint32, N)
	for i := 0; i < N; i++ {
		seq[i] = m.CPU.Reg(cpu.PC)
		if m.CPU.Step() != nil {
			seq = seq[:i]
			break
		}
	}
	// Find the loop: the most recent PC that recurs, and the period.
	last := map[uint32]int{}
	period := 0
	loopHead := uint32(0)
	for i, pc := range seq {
		if j, ok := last[pc]; ok {
			p := i - j
			if p > 4 && p < 4000 { // plausible loop body
				period = p
				loopHead = pc
				break
			}
		}
		last[pc] = i
	}
	fmt.Printf("loop head=%06X period=%d instrs\n", loopHead, period)

	// Print one full loop iteration starting at loopHead.
	start := -1
	for i, pc := range seq {
		if pc == loopHead {
			start = i
			break
		}
	}
	if start >= 0 && period > 0 {
		fmt.Println("--- one loop iteration ---")
		shown := map[uint32]bool{}
		for i := start; i < start+period && i < len(seq); i++ {
			pc := seq[i]
			d, _ := m.CPU.Disasm(pc)
			marker := ""
			if shown[pc] {
				marker = " (revisit)"
			}
			shown[pc] = true
			fmt.Printf("  %06X  %s%s\n", pc, d, marker)
		}
	}
}
