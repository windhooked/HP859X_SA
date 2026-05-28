// Command displayprobe boots the HP 8593A machine into its operating loop and
// records every MMIO write, then prints a histogram of write addresses and the
// distinct values seen at each. The goal is to discover which display path the
// firmware actually drives (SCI command interface at 0xFFF5FC vs. an HD63484
// ACRTC framebuffer) and what data format it sends.
//
// Usage:
//
//	go run ./cmd/displayprobe/ [cycles]   # default 30 000 000
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

// sciEvent is one ordered write to an SCI port (0x5FC command or 0x5FE data).
type sciEvent struct {
	off uint32 // 0x5FC or 0x5FE
	val uint32
}

// logMMIO wraps the real MMIO device and records every write.
type logMMIO struct {
	inner   *device.HP8593AMMIO
	base    uint32
	writes  map[uint32]int            // absolute addr -> count
	values  map[uint32]map[uint32]int // absolute addr -> value -> count
	enabled bool

	// streamCap, when > 0, records the first streamCap ordered SCI writes
	// (offsets 0x5FC / 0x5FE only) after logging is enabled.
	streamCap int
	stream    []sciEvent
}

func newLogMMIO(base uint32) *logMMIO {
	return &logMMIO{
		inner:  device.NewHP8593AMMIO(),
		base:   base,
		writes: make(map[uint32]int),
		values: make(map[uint32]map[uint32]int),
	}
}

func (l *logMMIO) Read(addr uint32, sz bus.Size) uint32 { return l.inner.Read(addr, sz) }

func (l *logMMIO) Write(addr uint32, sz bus.Size, val uint32) {
	l.inner.Write(addr, sz, val)
	if !l.enabled {
		return
	}
	abs := l.base + addr
	l.writes[abs]++
	if l.values[abs] == nil {
		l.values[abs] = make(map[uint32]int)
	}
	l.values[abs][val]++

	if l.streamCap > 0 && len(l.stream) < l.streamCap && (addr == 0x5FC || addr == 0x5FE) {
		l.stream = append(l.stream, sciEvent{off: addr, val: val})
	}
}

func main() {
	totalCycles := 30_000_000
	if len(os.Args) > 1 {
		n, err := strconv.Atoi(os.Args[1])
		if err != nil || n <= 0 {
			fmt.Fprintf(os.Stderr, "usage: displayprobe [cycles]\n")
			os.Exit(1)
		}
		totalCycles = n
	}

	img, err := romloader.LoadDir("hp8593a_eeproms")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	mmio := newLogMMIO(0xFFF000)
	mmio.streamCap = 80_000 // capture the first 80K ordered SCI writes (enough to span a full frame's worth of commands)

	b := &bus.Bus{}
	b.OnFault = func(addr uint32, sz bus.Size, write bool) uint32 { return 0 }
	b.Map(0x000000, uint32(len(img)), "ROM", bus.NewROM(img))
	b.Map(0xEF8000, 0x000100, "PIT", bus.NewRAM(0x000100))
	b.Map(0xFEC000, 0x004000, "TestRAM", bus.NewRAM(0x004000))
	b.Map(0xFF0000, 0x00F000, "RAM", bus.NewRAM(0x00F000))
	b.Map(0xFFF000, 0x001000, "MMIO", mmio)

	c, _ := musashi.New(b)
	c.Reset()

	const chunkCycles = 2000
	const breakThresh = 50
	const irqPeriod = 5
	const irqServiceCycles = 400
	const operatingPC = uint32(0xB000) // start logging once we're in the main loop

	lb := emutest.NewLoopBreaker(breakThresh)

	for done := 0; done < totalCycles; done += chunkCycles {
		c.Run(chunkCycles)
		pc := c.Reg(cpu.PC)
		lb.Check(pc, c.SetReg)

		// Enable logging only once the firmware has reached the operating loop,
		// so boot-time register setup does not swamp the histogram.
		if !mmio.enabled && pc >= operatingPC {
			mmio.enabled = true
		}

		chunk := done / chunkCycles
		if chunk%irqPeriod == 0 {
			c.SetIRQ(5)
			c.Run(irqServiceCycles)
			c.SetIRQ(0)
		}
	}

	fmt.Printf("=== MMIO writes during operating loop (final PC=%06X) ===\n", c.Reg(cpu.PC))

	addrs := make([]uint32, 0, len(mmio.writes))
	for a := range mmio.writes {
		addrs = append(addrs, a)
	}
	sort.Slice(addrs, func(i, j int) bool { return addrs[i] < addrs[j] })

	for _, a := range addrs {
		vals := mmio.values[a]
		// Summarise: number of distinct values, and up to 6 most-common.
		type vc struct {
			v uint32
			c int
		}
		list := make([]vc, 0, len(vals))
		for v, c := range vals {
			list = append(list, vc{v, c})
		}
		sort.Slice(list, func(i, j int) bool { return list[i].c > list[j].c })
		fmt.Printf("  %06X  writes=%-7d distinct=%-4d  top:", a, mmio.writes[a], len(vals))
		for i := 0; i < len(list) && i < 6; i++ {
			fmt.Printf(" %04X(%d)", list[i].v, list[i].c)
		}
		fmt.Println()
	}

	// Ordered SCI write stream — the actual display command/data protocol.
	// 'C' = command port 0x5FC, 'D' = data port 0x5FE. The in-band marker
	// 0x8000 in the data stream is a "move pen to (X,Y)" position command.
	// Runs of identical consecutive writes are folded with a ×N suffix so
	// long bulk fills (e.g. 0x4400 frame-buffer clears) don't dominate.
	fmt.Printf("\n=== SCI write stream (first %d writes; C=cmd@5FC D=data@5FE; ×N folds runs) ===\n", len(mmio.stream))
	type folded struct {
		port byte // 'C' or 'D'
		val  uint32
		n    int
	}
	runs := make([]folded, 0, 1024)
	for _, e := range mmio.stream {
		p := byte('D')
		if e.off == 0x5FC {
			p = 'C'
		}
		if len(runs) > 0 && runs[len(runs)-1].port == p && runs[len(runs)-1].val == e.val {
			runs[len(runs)-1].n++
			continue
		}
		runs = append(runs, folded{p, e.val, 1})
	}
	col := 0
	for _, r := range runs {
		annot := ""
		switch {
		case r.port == 'D' && r.val == 0x8000:
			annot = "<MOVE"
		case r.port == 'D' && r.val == 0x8801:
			annot = "<LINE"
		case r.port == 'D' && r.val == 0x9000:
			annot = "<ARCT"
		case r.port == 'D' && r.val == 0xCC00:
			annot = "<DOT"
		case r.port == 'D' && r.val == 0x1800:
			annot = "<WPTN"
		case r.port == 'D' && (r.val&0xFFE0) == 0x0800:
			annot = "<WPR"
		}
		if r.n > 1 {
			fmt.Printf("  %c:%04X×%-3d%-6s", r.port, r.val, r.n, annot)
		} else {
			fmt.Printf("  %c:%04X     %-6s", r.port, r.val, annot)
		}
		col++
		if col%5 == 0 {
			fmt.Println()
		}
	}
	fmt.Println()
	fmt.Printf("(stream length: %d writes folded to %d runs)\n", len(mmio.stream), len(runs))
}
