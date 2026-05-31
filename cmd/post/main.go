// Command post instruments the A16 power-on self-test (POST). It boots and
// captures: (a) whether the self-test body at 0x4998 executes, (b) every write
// to the self-test register block 0xFFF604..0xFFF7FF (PC, addr, value) — the
// PASS-flag latches f610/f612, the loopback data reg f700, control f604/f606 —
// and (c) every read of f780/f614/f616 (the status/loopback inputs the tests
// check). This tells us exactly which hardware to model so the POST passes.
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

	type ev struct{ pc, addr, val uint32 }
	var writes []ev
	readHist := map[uint32]int{} // addr -> count of reads in the block
	stEntries := 0
	prevPC := uint32(0)

	m.Bus.OnWrite = func(addr uint32, sz bus.Size, val uint32) {
		if addr >= 0xFFF604 && addr <= 0xFFF7FF {
			if len(writes) < 200 {
				writes = append(writes, ev{m.CPU.Reg(cpu.PC), addr, val})
			}
		}
	}
	m.Bus.OnRead = func(addr uint32, sz bus.Size, val uint32) {
		if addr >= 0xFFF604 && addr <= 0xFFF7FF {
			readHist[addr]++
		}
	}

	for c := 0; c < 165_000_000; c += 2000 {
		// sample PC to detect self-test entry
		for s := 0; s < 4; s++ {
			pc := m.CPU.Reg(cpu.PC)
			if prevPC != 0x4998 && pc == 0x4998 {
				stEntries++
			}
			prevPC = pc
			if m.CPU.Step() != nil {
				break
			}
		}
		m.CPU.Run(2000)
		lb.Check(m.CPU.Reg(cpu.PC), m.CPU.SetReg)
		if (c/2000)%5 == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Run(400)
			m.CPU.SetIRQ(0)
		}
	}

	fmt.Printf("self-test body @0x4998 entered: %d times\n\n", stEntries)
	fmt.Printf("writes to self-test register block 0xFFF604..7FF (first %d):\n", len(writes))
	for _, w := range writes {
		fmt.Printf("  PC %06X  [%06X] <= %04X\n", w.pc, w.addr, w.val&0xFFFF)
	}
	fmt.Println("\nreads of the block (addr -> count):")
	var addrs []uint32
	for a := range readHist {
		addrs = append(addrs, a)
	}
	sort.Slice(addrs, func(i, j int) bool { return addrs[i] < addrs[j] })
	for _, a := range addrs {
		fmt.Printf("  [%06X] x%d\n", a, readHist[a])
	}
}
