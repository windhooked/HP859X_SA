// dlpsched (phase 10): with both gates forced (active sweeping), histogram which
// DLP sources ARE scheduled — to characterize Gate 2 (why __GTTDRW isn't among them).
package main

import (
	"fmt"
	"sort"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
	"github.com/windhooked/HP859X_SA/pkg/emu/machine"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

func main() {
	rom, _ := romloader.LoadDir("hp8593a_eeproms")
	m, _ := machine.New8593A(rom)
	m.CPU.Reset()
	srcHist := map[uint32]int{}
	m.Bus.OnRead = func(a uint32, s bus.Size, v uint32) {
		if a == 0x000D18 {
			srcHist[m.CPU.Reg(cpu.A0)]++
		}
	}
	m.BootToOperating(190_000_000)
	for k := range srcHist { delete(srcHist, k) } // reset after boot
	m.MMIO.SweepActive = true
	for i := 0; i < 8_000_000; i++ {
		if uint8(m.Bus.Read(0xFFB0EC, bus.Byte)) != 0x31 { m.Bus.Write(0xFFB0EC, bus.Byte, 0x31) }
		if int16(m.Bus.Read(0xFFA9A0, bus.Word)) < 0 { m.Bus.Write(0xFFA9A0, bus.Word, 0x0080) }
		if m.CPU.Step() != nil { break }
		if i%2000 == 0 { m.CPU.SetIRQ(5); m.CPU.Step(); m.CPU.SetIRQ(0) }
		if i%1500 == 0 { m.CPU.SetIRQ(6); m.CPU.Step(); m.CPU.SetIRQ(0) }
	}
	type e struct{ s uint32; n int }
	var es []e
	for s, n := range srcHist { es = append(es, e{s, n}) }
	sort.Slice(es, func(i, j int) bool { return es[i].n > es[j].n })
	fmt.Printf("DLP sources scheduled DURING forced sweep: %d distinct\n", len(es))
	for i := 0; i < len(es) && i < 20; i++ {
		fmt.Printf("  0x%06X x%d\n", es[i].s, es[i].n)
	}
	fmt.Printf("trace-draw 0x6595A: %d\n", srcHist[0x6595A])
}
