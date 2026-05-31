// Command annuncode maps the annunciator add/remove API. fcn.e7f0(D0=code) ADDS
// an annunciator; fcn.e87e(D0=code) REMOVES it. Using CPU.RunUntil it stops at
// each call during boot and records the code + caller PC (the status check that
// gates it). The net set (added, not removed) = the visible annunciators; each
// caller PC localizes the hardware-status condition to model.
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

func scan(target uint32, label string) {
	rom, _ := romloader.LoadDir("hp8593a_eeproms")
	m, _ := machine.New8593A(rom)
	m.CPU.Reset()
	lb := emutest.NewLoopBreaker(50)
	type ev struct{ code, caller uint32 }
	hits := map[string]*ev{}
	order := []string{}
	for done := 0; done < 165_000_000; {
		n, stopped := m.CPU.RunUntil(2000, target)
		done += n
		if stopped {
			code := m.CPU.Reg(cpu.D0) & 0xFF
			// RunUntil stops AFTER the entry `link A6,#-2` executed, so the jsr
			// return address sits at A6+4 (above the saved old A6).
			caller := m.Bus.Read(m.CPU.Reg(cpu.A6)+4, bus.Long) - 6
			key := fmt.Sprintf("%02X@%06X", code, caller)
			if hits[key] == nil {
				hits[key] = &ev{code, caller}
				order = append(order, key)
			}
			m.CPU.Step() // move past the entry so we catch the next call
			continue
		}
		lb.Check(m.CPU.Reg(cpu.PC), m.CPU.SetReg)
		if (done/2000)%5 == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Run(400)
			m.CPU.SetIRQ(0)
		}
	}
	sort.Slice(order, func(i, j int) bool { return hits[order[i]].code < hits[order[j]].code })
	fmt.Printf("=== %s (fcn.%06X) call sites: code -> caller ===\n", label, target)
	for _, k := range order {
		e := hits[k]
		d, _ := m.CPU.Disasm(e.caller)
		fmt.Printf("  code 0x%02X  caller %06X  %s\n", e.code, e.caller, d)
	}
}

func main() {
	scan(0xE7F0, "ADD annunciator")
	fmt.Println()
	scan(0xE87E, "REMOVE annunciator")
}
