// Command hwprobe maps every hardware register the firmware touches from reset
// through self-test / auto-cal: it logs all reads and writes to the I/O regions
// (0x200000 RF/IF, 0xEF0000 I/O, 0xFFF000 MMIO) and any unmapped address, with a
// minimal RF/IF mock so the full self-test runs. The read histogram + value
// distributions show which registers the self-test/cal probes and what they
// currently return — the foundation for the pragmatic "satisfy the checks" work.
//
// Usage:
//
//	go run ./cmd/hwprobe/ [a3c-hex] [cycles]
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

// logDev wraps a real bus.Device, logging reads/writes per absolute address.
type logDev struct {
	base   uint32
	inner  bus.Device
	reads  map[uint32]map[uint32]int // addr -> value -> count
	writes map[uint32]map[uint32]int
}

func newLogDev(base uint32, inner bus.Device) *logDev {
	return &logDev{base: base, inner: inner,
		reads: map[uint32]map[uint32]int{}, writes: map[uint32]map[uint32]int{}}
}

func (l *logDev) Read(addr uint32, sz bus.Size) uint32 {
	v := l.inner.Read(addr, sz)
	abs := l.base + addr
	if l.reads[abs] == nil {
		l.reads[abs] = map[uint32]int{}
	}
	l.reads[abs][v]++
	return v
}

func (l *logDev) Write(addr uint32, sz bus.Size, val uint32) {
	l.inner.Write(addr, sz, val)
	abs := l.base + addr
	if l.writes[abs] == nil {
		l.writes[abs] = map[uint32]int{}
	}
	l.writes[abs][val]++
}

// rfifMock returns a3c at offset 0xA3C, else RAM-backed.
type rfifMock struct {
	b   [0x10000]byte
	a3c uint32
}

func (r *rfifMock) Read(addr uint32, sz bus.Size) uint32 {
	if addr == 0xA3C {
		return r.a3c
	}
	switch sz {
	case bus.Byte:
		return uint32(r.b[addr])
	case bus.Word:
		return uint32(r.b[addr])<<8 | uint32(r.b[addr+1])
	default:
		return uint32(r.b[addr])<<24 | uint32(r.b[addr+1])<<16 | uint32(r.b[addr+2])<<8 | uint32(r.b[addr+3])
	}
}
func (r *rfifMock) Write(addr uint32, sz bus.Size, val uint32) {
	switch sz {
	case bus.Byte:
		r.b[addr] = byte(val)
	case bus.Word:
		r.b[addr] = byte(val >> 8)
		r.b[addr+1] = byte(val)
	default:
		r.b[addr] = byte(val >> 24)
		r.b[addr+1] = byte(val >> 16)
		r.b[addr+2] = byte(val >> 8)
		r.b[addr+3] = byte(val)
	}
}

func main() {
	a3c := uint32(1)
	cycles := 40_000_000
	if len(os.Args) > 1 {
		if n, err := strconv.ParseUint(os.Args[1], 16, 32); err == nil {
			a3c = uint32(n)
		}
	}
	if len(os.Args) > 2 {
		if n, err := strconv.Atoi(os.Args[2]); err == nil {
			cycles = n
		}
	}

	img, _ := romloader.LoadDir("hp8593a_eeproms")
	rf := newLogDev(0x200000, &rfifMock{a3c: a3c})
	pit := newLogDev(0xEF8000, bus.NewRAM(0x100))
	fp := newLogDev(0xEF4000, device.NewFrontPanel())
	mm := newLogDev(0xFFF000, device.NewHP8593AMMIO()) // real SCI/sweep overrides

	faults := map[uint32]int{}
	b := &bus.Bus{}
	b.OnFault = func(addr uint32, sz bus.Size, w bool) uint32 { faults[addr]++; return 0 }
	b.Map(0x000000, uint32(len(img)), "ROM", bus.NewROM(img))
	b.Map(0x200000, 0x10000, "RFIF", rf)
	b.Map(0xEF4000, 0x20, "FrontPanel", fp)
	b.Map(0xEF8000, 0x100, "PIT", pit)
	b.Map(0xFEC000, 0x4000, "TestRAM", bus.NewRAM(0x4000))
	b.Map(0xFF0000, 0xF000, "RAM", bus.NewRAM(0xF000))
	b.Map(device.MMIOBase, device.MMIOSize, "MMIO", mm)

	c, _ := musashi.New(b)
	c.Reset()
	lb := emutest.NewLoopBreaker(50)
	for done := 0; done < cycles; done += 2000 {
		c.Run(2000)
		lb.Check(c.Reg(cpu.PC), c.SetReg)
		if (done/2000)%5 == 0 {
			c.SetIRQ(5)
			c.Run(400)
			c.SetIRQ(0)
		}
	}

	fmt.Printf("a3c=%X cycles=%d finalPC=%06X\n", a3c, cycles, c.Reg(cpu.PC))
	for _, d := range []*logDev{rf, fp, pit, mm} {
		report(d)
	}
	fmt.Println("=== unmapped (OnFault) reads/writes ===")
	fa := keys(faults)
	for _, a := range fa {
		fmt.Printf("  %06X  x%d\n", a, faults[a])
	}
}

func keys(m map[uint32]int) []uint32 {
	ks := make([]uint32, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Slice(ks, func(i, j int) bool { return ks[i] < ks[j] })
	return ks
}

func report(d *logDev) {
	addrs := map[uint32]bool{}
	for a := range d.reads {
		addrs[a] = true
	}
	for a := range d.writes {
		addrs[a] = true
	}
	ks := make([]uint32, 0, len(addrs))
	for a := range addrs {
		ks = append(ks, a)
	}
	sort.Slice(ks, func(i, j int) bool { return ks[i] < ks[j] })
	fmt.Printf("=== region %06X (%d distinct addrs) ===\n", d.base, len(ks))
	for _, a := range ks {
		rc := total(d.reads[a])
		wc := total(d.writes[a])
		fmt.Printf("  %06X  R=%-6d W=%-6d", a, rc, wc)
		if rc > 0 {
			fmt.Printf("  Rval:%s", topVals(d.reads[a]))
		}
		if wc > 0 {
			fmt.Printf("  Wval:%s", topVals(d.writes[a]))
		}
		fmt.Println()
	}
}

func total(m map[uint32]int) int {
	n := 0
	for _, c := range m {
		n += c
	}
	return n
}

func topVals(m map[uint32]int) string {
	type vc struct {
		v uint32
		c int
	}
	l := make([]vc, 0, len(m))
	for v, c := range m {
		l = append(l, vc{v, c})
	}
	sort.Slice(l, func(i, j int) bool { return l[i].c > l[j].c })
	s := ""
	for i := 0; i < len(l) && i < 4; i++ {
		s += fmt.Sprintf(" %X(%d)", l[i].v, l[i].c)
	}
	return s
}
