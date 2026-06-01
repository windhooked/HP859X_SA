// Command unmapped audits the firmware's bus accesses for any hardware we do NOT
// model — the place a real-time-clock chip (or any unmodeled peripheral) would
// reveal itself. It runs a long boot and reports three categories:
//
//  1. TRULY UNMAPPED accesses (bus OnFault) — addresses hitting no device.
//  2. MAPPED-BUT-STUBBED device ranges (MC68230 PIT timer @0xEF8000, front-panel
//     µC @0xEF4000) — registers we back with zeroed RAM.
//  3. MMIO offsets (0xFFF000-0xFFFFFF) and the COUNT of distinct values each
//     returns — a live RTC/counter register returns many increasing values;
//     a constant stub returns one.
//
// Conclusion from a 200M-cycle Rev L boot: no RTC. See the RTC survey in
// docs/rom_annotations.md.
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

	type acc struct {
		rd, wr int
		vals   map[uint32]bool
		pc     uint32
	}
	unmapped := map[uint32]*acc{}
	stub := map[uint32]*acc{} // PIT + front panel
	mmio := map[uint32]*acc{} // 0xFFF000-0xFFFFFF
	mk := func(mp map[uint32]*acc, a uint32) *acc {
		s := mp[a]
		if s == nil {
			s = &acc{vals: map[uint32]bool{}, pc: m.CPU.Reg(cpu.PC)}
			mp[a] = s
		}
		return s
	}
	note := func(mp map[uint32]*acc, a uint32, write bool, val uint32) {
		s := mk(mp, a)
		if write {
			s.wr++
		} else {
			s.rd++
			if len(s.vals) < 64 {
				s.vals[val] = true
			}
		}
	}
	isStub := func(a uint32) bool {
		return (a >= 0xEF4000 && a <= 0xEF401F) || (a >= 0xEF8000 && a <= 0xEF80FF)
	}
	m.Bus.OnFault = func(addr uint32, sz bus.Size, write bool) uint32 {
		note(unmapped, addr, write, 0)
		switch sz {
		case bus.Byte:
			return 0xFF
		case bus.Word:
			return 0xFFFF
		default:
			return 0xFFFFFFFF
		}
	}
	m.Bus.OnRead = func(addr uint32, sz bus.Size, val uint32) {
		if isStub(addr) {
			note(stub, addr, false, val)
		} else if addr >= 0xFFF000 && addr <= 0xFFFFFF {
			note(mmio, addr, false, val)
		}
	}
	m.Bus.OnWrite = func(addr uint32, sz bus.Size, val uint32) {
		if isStub(addr) {
			note(stub, addr, true, val)
		} else if addr >= 0xFFF000 && addr <= 0xFFFFFF {
			note(mmio, addr, true, val)
		}
	}

	const total = 200_000_000
	for done := 0; done < total; done += 2000 {
		m.CPU.Run(2000)
		lb.Check(m.CPU.Reg(cpu.PC), m.CPU.SetReg)
		if (done/2000)%5 == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Run(400)
			m.CPU.SetIRQ(0)
		}
	}

	dump := func(title string, mp map[uint32]*acc, showVals bool) {
		var as []uint32
		for a := range mp {
			as = append(as, a)
		}
		sort.Slice(as, func(i, j int) bool { return as[i] < as[j] })
		fmt.Printf("\n=== %s (%d distinct) ===\n", title, len(as))
		for _, a := range as {
			s := mp[a]
			extra := ""
			if showVals {
				extra = fmt.Sprintf(" distinct=%d", len(s.vals))
				if len(s.vals) >= 8 {
					extra += "  <-- VARIES (counter-like)"
				}
			}
			fmt.Printf("  %06X  rd=%-7d wr=%-7d pc=%06X%s\n", a, s.rd, s.wr, s.pc, extra)
		}
	}
	dump("TRULY UNMAPPED (no device)", unmapped, false)
	dump("MAPPED-BUT-STUBBED (PIT 68230 / front panel)", stub, true)
	dump("MMIO 0xFFF000-0xFFFFFF (counter-variety)", mmio, true)
}
