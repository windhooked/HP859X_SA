// Command keyprobe boots the HP 8593A into its operating loop and histograms
// MMIO *reads* (across the 0xFFF000 window and the 0xEF8000 PIT stub) to find
// which registers the firmware polls for front-panel keyboard / RPG input.
// High read counts in the idle operating loop = the keyboard-scan registers.
//
// Usage:
//
//	go run ./cmd/keyprobe/ [cycles]   # default 40 000 000
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

// readLogger wraps a bus.Device and counts reads (and the distinct values
// returned) per absolute address, once enabled.
type readLogger struct {
	inner   bus.Device
	base    uint32
	reads   map[uint32]int
	values  map[uint32]map[uint32]int
	enabled *bool
}

func newReadLogger(base uint32, inner bus.Device, en *bool) *readLogger {
	return &readLogger{
		inner:   inner,
		base:    base,
		reads:   make(map[uint32]int),
		values:  make(map[uint32]map[uint32]int),
		enabled: en,
	}
}

func (l *readLogger) Read(addr uint32, sz bus.Size) uint32 {
	v := l.inner.Read(addr, sz)
	if *l.enabled {
		abs := l.base + addr
		l.reads[abs]++
		if l.values[abs] == nil {
			l.values[abs] = make(map[uint32]int)
		}
		l.values[abs][v]++
	}
	return v
}

func (l *readLogger) Write(addr uint32, sz bus.Size, val uint32) { l.inner.Write(addr, sz, val) }

func (l *readLogger) report(name string) {
	addrs := make([]uint32, 0, len(l.reads))
	for a := range l.reads {
		addrs = append(addrs, a)
	}
	sort.Slice(addrs, func(i, j int) bool { return l.reads[addrs[i]] > l.reads[addrs[j]] })

	fmt.Printf("=== %s reads (operating loop), by frequency ===\n", name)
	for _, a := range addrs {
		vals := l.values[a]
		type vc struct {
			v uint32
			c int
		}
		list := make([]vc, 0, len(vals))
		for v, c := range vals {
			list = append(list, vc{v, c})
		}
		sort.Slice(list, func(i, j int) bool { return list[i].c > list[j].c })
		fmt.Printf("  %06X  reads=%-8d distinct=%-4d top:", a, l.reads[a], len(vals))
		for i := 0; i < len(list) && i < 6; i++ {
			fmt.Printf(" %04X(%d)", list[i].v, list[i].c)
		}
		fmt.Println()
	}
}

func main() {
	totalCycles := 40_000_000
	if len(os.Args) > 1 {
		if n, err := strconv.Atoi(os.Args[1]); err == nil && n > 0 {
			totalCycles = n
		}
	}

	img, err := romloader.LoadDir("hp8593a_eeproms")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	var enabled bool
	pit := newReadLogger(0xEF8000, bus.NewRAM(0x000100), &enabled)
	mmio := newReadLogger(0xFFF000, device.NewHP8593AMMIO(), &enabled)

	// Histogram unmapped (OnFault) accesses — the front-panel/keyboard
	// controller is likely at an address not yet mapped (e.g. 0xEF4000).
	faultRd := make(map[uint32]int)
	faultWr := make(map[uint32]int)

	b := &bus.Bus{}
	b.OnFault = func(addr uint32, sz bus.Size, write bool) uint32 {
		if enabled {
			if write {
				faultWr[addr]++
			} else {
				faultRd[addr]++
			}
		}
		return 0
	}
	b.Map(0x000000, uint32(len(img)), "ROM", bus.NewROM(img))
	b.Map(0xEF8000, 0x000100, "PIT", pit)
	b.Map(0xFEC000, 0x004000, "TestRAM", bus.NewRAM(0x004000))
	b.Map(0xFF0000, 0x00F000, "RAM", bus.NewRAM(0x00F000))
	b.Map(0xFFF000, 0x001000, "MMIO", mmio)

	c, _ := musashi.New(b)
	c.Reset()

	const chunkCycles = 2000
	const irqPeriod = 5
	const irqServiceCycles = 400
	const operatingPC = uint32(0xB000)
	lb := emutest.NewLoopBreaker(50)

	for done := 0; done < totalCycles; done += chunkCycles {
		c.Run(chunkCycles)
		pc := c.Reg(cpu.PC)
		lb.Check(pc, c.SetReg)
		if !enabled && pc >= operatingPC {
			enabled = true
		}
		if (done/chunkCycles)%irqPeriod == 0 {
			c.SetIRQ(5)
			c.Run(irqServiceCycles)
			c.SetIRQ(0)
		}
	}

	fmt.Printf("final PC=%06X\n\n", c.Reg(cpu.PC))
	mmio.report("MMIO 0xFFF000")
	fmt.Println()
	pit.report("PIT 0xEF8000")
	fmt.Println()
	reportFaults("unmapped reads", faultRd)
	fmt.Println()
	reportFaults("unmapped writes", faultWr)
}

func reportFaults(name string, m map[uint32]int) {
	addrs := make([]uint32, 0, len(m))
	for a := range m {
		addrs = append(addrs, a)
	}
	sort.Slice(addrs, func(i, j int) bool { return m[addrs[i]] > m[addrs[j]] })
	fmt.Printf("=== %s (operating loop), by frequency ===\n", name)
	for _, a := range addrs {
		fmt.Printf("  %06X  count=%d\n", a, m[a])
	}
}
