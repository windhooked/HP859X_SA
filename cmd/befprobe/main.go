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
	wbef := map[uint32]uint32{}
	rb034 := map[uint32]int{}
	m.Bus.OnWrite = func(a uint32, sz bus.Size, v uint32) {
		if a == 0xFFBEF6 {
			wbef[m.CPU.Reg(cpu.PC)] = v & 0xFFFF
		}
	}
	m.Bus.OnRead = func(a uint32, sz bus.Size, v uint32) {
		if a >= 0xFFB034 && a <= 0xFFB035 {
			rb034[m.CPU.Reg(cpu.PC)]++
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
	fmt.Printf("after boot: 0xFFBEF6=%04X  0xFFB034=%04X\n",
		uint16(m.Bus.Read(0xFFBEF6, bus.Word)), uint16(m.Bus.Read(0xFFB034, bus.Word)))
	fmt.Println("writers of 0xFFBEF6 (PC -> last value):")
	for pc, v := range wbef {
		d, _ := m.CPU.Disasm(pc)
		fmt.Printf("  %06X val=%04X  %s\n", pc, v, d)
	}
	fmt.Println("readers of 0xFFB034:")
	var ps []uint32
	for p := range rb034 {
		ps = append(ps, p)
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i] < ps[j] })
	for _, p := range ps {
		d, _ := m.CPU.Disasm(p)
		fmt.Printf("  %06X x%-4d %s\n", p, rb034[p], d)
	}
}
