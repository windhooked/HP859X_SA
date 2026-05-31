// Command failcode cracks the power-up self-test FAIL display ("FAIL: " at ROM
// 0x19501). Boot with a READ watchpoint on the FAIL string; at the first drawer
// fetch, walk the A6 frame-pointer chain to backtrace the callers — the self-
// test reporter up the stack holds the failed-test bitmask. Disassemble each
// return site so we can read off where the status word lives.
package main

import (
	"fmt"

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
	inRAM := func(a uint32) bool { return a >= 0xFF0000 && a <= 0xFFEFFF }

	const failStr = 0x19501
	done := false
	m.Bus.OnRead = func(addr uint32, sz bus.Size, val uint32) {
		if done || addr < failStr || addr > failStr+2 {
			return
		}
		done = true
		pc := m.CPU.Reg(cpu.PC)
		fmt.Printf("FAIL drawer fetch at PC=%06X\n", pc)
		fmt.Print("registers: ")
		for i := 0; i < 16; i++ {
			fmt.Printf("%s=%08X ", []string{"D0", "D1", "D2", "D3", "D4", "D5", "D6", "D7", "A0", "A1", "A2", "A3", "A4", "A5", "A6", "A7"}[i], m.CPU.Reg(cpu.Reg(i)))
			if i == 7 {
				fmt.Print("\n           ")
			}
		}
		fmt.Println()
		fmt.Println("\nbacktrace (A6 frame chain — return sites):")
		a6 := m.CPU.Reg(cpu.A6)
		for depth := 0; depth < 12 && inRAM(a6); depth++ {
			ret := rdL(a6 + 4)
			prev := rdL(a6)
			d, _ := m.CPU.Disasm(ret - 6) // the jsr/bsr is ~6 bytes before the return
			fmt.Printf("  #%-2d ret=%06X frame=%06X   call: %s\n", depth, ret, a6, d)
			if prev <= a6 || !inRAM(prev) {
				break
			}
			a6 = prev
		}
		// also dump the drawer's caller window (PC-relative) — find the value source
		fmt.Println("\ndrawer @0xBE60 region:")
		a := uint32(0xBE48)
		for a < 0xBE90 {
			ds, n := m.CPU.Disasm(a)
			mk := "   "
			if a == pc {
				mk = ">> "
			}
			fmt.Printf("  %s%06X  %s\n", mk, a, ds)
			if n == 0 {
				n = 2
			}
			a += n
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
	if !done {
		fmt.Println("FAIL string never fetched")
	}
}
