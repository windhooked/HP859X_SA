// dlpfresh: a fresh, debugger-driven look at the post-boot steady state — does
// the firmware reach the C operating loop fcn.18568, what loop does it actually
// spin in, and where do the DLP display-source dispatches get gated. It boots,
// records the first time each landmark PC executes, then single-steps a window
// building a 1KB-page PC histogram + exact landmark hit counts + gate-cell snapshots.
package main

import (
	"fmt"
	"sort"

	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
	"github.com/windhooked/HP859X_SA/pkg/emu/machine"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

var landmarks = map[uint32]string{
	0x18568: "op-loop ENTER fcn.18568",
	0x18a88: "op-loop CLOSE (bra 18568)",
	0x18f3e: "deep parser jsr (key path)",
	0x18f42: "deep key-flag bclr",
	0x568f6: "fcn.568F6 (annunc chain)",
	0x11df4: "fcn.11DF4 (annunc/checksum)",
	0x34ee8: "DLP interp step fcn.34EE8",
	0x349b6: "DLP scheduler fcn.349B6",
	0x5ecee: "trace-state fcn.5ECEE",
	0x5ed7e: "fcn.5ED7E (trace src/cal)",
	0x65986: "__GTTDRW trace-draw",
	0x66296: "__GGTSWSW sweep-state",
	0x59718: "date display fcn.59718",
	0x22532: "freeze loop 0x22532",
}

var cells = []struct {
	addr uint32
	name string
}{
	{0xFFB0A0, "b0a0"}, {0xFFBEFA, "befa"}, {0xFFBC64, "bc64"}, {0xFF9FB4, "9fb4"},
	{0xFFBF26, "bf26"}, {0xFFB1E0, "b1e0"}, {0xFFB212, "b212"}, {0xFFAD7C, "ad7c"},
	{0xFFBF34, "bf34"}, {0xFFBC67, "bc67"}, {0xFFF300, "f300"},
}

func snapCells(m *machine.Machine) string {
	s := ""
	for _, c := range cells {
		s += fmt.Sprintf("%s=%04x ", c.name, uint32(m.Bus.Read(c.addr, 2)))
	}
	return s
}

func main() {
	rom, _ := romloader.LoadDir("hp8593a_eeproms")
	m, _ := machine.New8593A(rom)
	m.CPU.Reset()

	firstHit := map[uint32]int{}
	hits := map[uint32]int{}
	pcPage := map[uint32]int{}

	// --- Boot phase: chunked, watch for first landmark hits via fine stepping
	// at chunk granularity is too coarse; instead boot normally, then profile. ---
	m.BootToOperating(250_000_000)
	fmt.Printf("post-boot PC=%06X\n", m.CPU.Reg(cpu.PC))
	fmt.Printf("cells @ boot: %s\n\n", snapCells(m))

	// --- Profile window: single-step + capture the RETURN ADDRESS each time the
	// deadline-timeout helper fcn.4824 is entered — names the stuck poll loop. ---
	const steps = 4_000_000
	pollCallers := map[uint32]int{}
	for i := 0; i < steps; i++ {
		pc := m.CPU.Reg(cpu.PC)
		pcPage[pc>>10]++
		if pc == 0x4824 {
			ret := uint32(m.Bus.Read(m.CPU.Reg(cpu.A7), 4))
			pollCallers[ret]++
		}
		if _, ok := landmarks[pc]; ok {
			hits[pc]++
			if _, seen := firstHit[pc]; !seen {
				firstHit[pc] = i
			}
		}
		m.CPU.Run(1)
		if i%50000 == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Run(300)
			m.CPU.SetIRQ(0)
		}
	}
	fmt.Printf("=== fcn.4824 timeout-poll callers (who is polling) ===\n")
	type cl struct {
		a uint32
		n int
	}
	var cls []cl
	for a, n := range pollCallers {
		cls = append(cls, cl{a, n})
	}
	sort.Slice(cls, func(i, j int) bool { return cls[i].n > cls[j].n })
	for i, e := range cls {
		if i >= 10 {
			break
		}
		fmt.Printf("  ret=%06X  x%d\n", e.a, e.n)
	}
	fmt.Println()

	fmt.Printf("=== landmark hits over %d steps ===\n", steps)
	var lm []uint32
	for a := range landmarks {
		lm = append(lm, a)
	}
	sort.Slice(lm, func(i, j int) bool { return lm[i] < lm[j] })
	for _, a := range lm {
		first := "-"
		if f, ok := firstHit[a]; ok {
			first = fmt.Sprintf("@step %d", f)
		}
		fmt.Printf("  %06X  x%-8d %-28s %s\n", a, hits[a], landmarks[a], first)
	}

	fmt.Printf("\n=== top PC pages (1KB) ===\n")
	type pg struct {
		p uint32
		n int
	}
	var pgs []pg
	for p, n := range pcPage {
		pgs = append(pgs, pg{p, n})
	}
	sort.Slice(pgs, func(i, j int) bool { return pgs[i].n > pgs[j].n })
	for i, e := range pgs {
		if i >= 14 {
			break
		}
		fmt.Printf("  %06X-%06X  %6.2f%%  (%d)\n", e.p<<10, (e.p<<10)+0x3FF, 100*float64(e.n)/float64(steps), e.n)
	}
	fmt.Printf("\ncells @ end: %s\n", snapCells(m))
}
