// Command adcprobe finds the ADC-cal code by execution tracing: it wraps the
// MMIO device and records the CPU PC at each access to the ADC sample register
// (0xFFF200) and candidate input/gain control registers, during a boot with the
// RF/IF region mapped (so the cal runs). The PC histograms point straight at the
// cal routines (reference select + read + check) to reverse-engineer.
//
// Usage:
//
//	go run ./cmd/adcprobe/ [a3c-hex]
package main

import (
	"fmt"
	"os"
	"sort"
	"strconv"

	"github.com/windhooked/HP859X_SA/internal/emutest"
	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
	musashi "github.com/windhooked/HP859X_SA/pkg/emu/cpu/musashi"
	"github.com/windhooked/HP859X_SA/pkg/emu/device"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

// watched MMIO offsets (relative to 0xFFF000) — ADC sample + candidate controls.
var watch = map[uint32]string{
	0x200: "ADC/sweep f200", 0x716: "ctl f716", 0x618: "ctl f618",
	0x610: "hpib f610", 0x612: "hpib f612", 0x71a: "ctl f71a", 0x70a: "ctl f70a",
}

type traceMMIO struct {
	inner *device.HP8593AMMIO
	cpu   *musashi.CPU
	rd    map[uint32]map[uint32]int // offset -> pc -> count
	wr    map[uint32]map[uint32]int
}

func (m *traceMMIO) pc() uint32 {
	if m.cpu == nil {
		return 0
	}
	return m.cpu.Reg(cpu.PC)
}
func (m *traceMMIO) Read(a uint32, s bus.Size) uint32 {
	if _, ok := watch[a]; ok {
		if m.rd[a] == nil {
			m.rd[a] = map[uint32]int{}
		}
		m.rd[a][m.pc()]++
	}
	return m.inner.Read(a, s)
}
func (m *traceMMIO) Write(a uint32, s bus.Size, v uint32) {
	if _, ok := watch[a]; ok {
		if m.wr[a] == nil {
			m.wr[a] = map[uint32]int{}
		}
		m.wr[a][m.pc()]++
	}
	m.inner.Write(a, s, v)
}

type rfif struct {
	b   [0x10000]byte
	a3c uint32
}

func (r *rfif) Read(a uint32, s bus.Size) uint32 {
	if a == 0xA3C {
		return r.a3c
	}
	return 0
}
func (r *rfif) Write(a uint32, s bus.Size, v uint32) {}

func main() {
	a3c := uint32(2)
	if len(os.Args) > 1 {
		if n, err := strconv.ParseUint(os.Args[1], 16, 32); err == nil {
			a3c = uint32(n)
		}
	}
	img, _ := romloader.LoadDir("hp8593a_eeproms")
	tm := &traceMMIO{inner: device.NewHP8593AMMIO(), rd: map[uint32]map[uint32]int{}, wr: map[uint32]map[uint32]int{}}

	b := &bus.Bus{}
	b.OnFault = func(a uint32, s bus.Size, w bool) uint32 { return 0 }
	b.Map(0x000000, uint32(len(img)), "ROM", bus.NewROM(img))
	b.Map(0x200000, 0x10000, "RFIF", &rfif{a3c: a3c})
	b.Map(0xEF4000, 0x20, "FrontPanel", device.NewFrontPanel())
	b.Map(0xEF8000, 0x100, "PIT", bus.NewRAM(0x100))
	b.Map(0xFEC000, 0x4000, "TestRAM", bus.NewRAM(0x4000))
	b.Map(0xFF0000, 0xF000, "RAM", bus.NewRAM(0xF000))
	b.Map(device.MMIOBase, device.MMIOSize, "MMIO", tm)

	c, _ := musashi.New(b)
	tm.cpu = c
	c.Reset()
	lb := emutest.NewLoopBreaker(50)
	for done := 0; done < 40_000_000; done += 2000 {
		c.Run(2000)
		lb.Check(c.Reg(cpu.PC), c.SetReg)
		if (done/2000)%5 == 0 {
			c.SetIRQ(5)
			c.Run(400)
			c.SetIRQ(0)
		}
	}

	offs := make([]uint32, 0, len(watch))
	for o := range watch {
		offs = append(offs, o)
	}
	sort.Slice(offs, func(i, j int) bool { return offs[i] < offs[j] })
	for _, o := range offs {
		fmt.Printf("=== %s (0xFFF%03X) ===\n", watch[o], o)
		dumpPCs("  R", tm.rd[o])
		dumpPCs("  W", tm.wr[o])
	}
}

func dumpPCs(tag string, m map[uint32]int) {
	if len(m) == 0 {
		return
	}
	type pc struct {
		p uint32
		c int
	}
	l := make([]pc, 0, len(m))
	for p, n := range m {
		l = append(l, pc{p, n})
	}
	sort.Slice(l, func(i, j int) bool { return l[i].c > l[j].c })
	for i := 0; i < len(l) && i < 8; i++ {
		fmt.Printf("%s pc~%06X x%d\n", tag, l[i].p, l[i].c)
	}
}
