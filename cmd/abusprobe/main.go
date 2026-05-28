// Command abusprobe records every write to the analog-bus select register
// (0xFFF75C) and every read from the data register (0xFFF75E) during the
// post-boot operating loop. Output: a stream of (select, data_returned)
// pairs along with the PC that issued each access, so we can identify
// which select values the firmware uses and what return values would
// unblock it (vs. the current always-0x06 / sometimes-0).
//
// Usage:
//
//	go run ./cmd/abusprobe/ [cycles]   # default 100_000_000 post-boot
//
// The output groups events by PC + select; the bus-side responder is the
// existing pkg/emu/device.HP8593AMMIO so the readings reflect the same
// model used in all other tests.
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

// abusEvent: one access to either 0x75C (write of a select) or 0x75E
// (read returning a data value).
type abusEvent struct {
	kind    byte   // 'W' = write to 75C (select); 'R' = read from 75E
	pc      uint32 // CPU PC at the time
	val     uint32 // value written or read
	curSel  uint16 // current select for context on read events
}

// abusTracer wraps the real HP8593AMMIO and intercepts 0x75C writes +
// 0x75E reads. It also keeps the "last select" so each read can be
// paired with its select for analysis.
type abusTracer struct {
	inner   *device.HP8593AMMIO
	pcFn    func() uint32
	curSel  uint16
	events  []abusEvent
	enabled bool
}

func newAbusTracer() *abusTracer {
	return &abusTracer{inner: device.NewHP8593AMMIO()}
}

func (a *abusTracer) setPCFunc(fn func() uint32) { a.pcFn = fn }

func (a *abusTracer) Read(addr uint32, sz bus.Size) uint32 {
	v := a.inner.Read(addr, sz)
	if a.enabled && addr == 0x75E && sz == bus.Word {
		ev := abusEvent{kind: 'R', val: v, curSel: a.curSel}
		if a.pcFn != nil {
			ev.pc = a.pcFn()
		}
		a.events = append(a.events, ev)
	}
	return v
}

func (a *abusTracer) Write(addr uint32, sz bus.Size, val uint32) {
	a.inner.Write(addr, sz, val)
	if a.enabled && addr == 0x75C && sz == bus.Word {
		a.curSel = uint16(val)
		ev := abusEvent{kind: 'W', val: val}
		if a.pcFn != nil {
			ev.pc = a.pcFn()
		}
		a.events = append(a.events, ev)
	}
}

func main() {
	postCycles := 100_000_000
	if len(os.Args) > 1 {
		if n, err := strconv.Atoi(os.Args[1]); err == nil && n > 0 {
			postCycles = n
		}
	}

	rom, err := romloader.LoadDir("hp8593a_eeproms")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	b := &bus.Bus{}
	b.OnFault = func(addr uint32, sz bus.Size, write bool) uint32 { return 0 }

	calNVRAM := device.NewCalNVRAM()
	calNVRAM.Synthesize()
	mmio := newAbusTracer()

	b.Map(0x000000, uint32(len(rom)), "ROM", bus.NewROM(rom))
	b.Map(device.CalNVRAMBase, device.CalNVRAMSize, "CalNVRAM", calNVRAM)
	b.Map(0x2FC000, 0x004000, "CalRAM", bus.NewRAM(0x004000))
	b.Map(0xEF8000, 0x000100, "PIT", bus.NewRAM(0x000100))
	b.Map(device.FrontPanelBase, device.FrontPanelSize, "FrontPanel", device.NewFrontPanel())
	b.Map(0xFEC000, 0x004000, "TestRAM", bus.NewRAM(0x004000))
	b.Map(0xFF0000, 0x00F000, "RAM", bus.NewRAM(0x00F000))
	b.Map(device.MMIOBase, device.MMIOSize, "MMIO", mmio)

	c, err := musashi.New(b)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	mmio.setPCFunc(func() uint32 { return c.Reg(cpu.PC) })

	c.Reset()

	// Boot
	const (
		bootChunkCycles    = 2000
		bootBreakThresh    = 50
		bootIRQPeriod      = 5
		bootIRQServiceCost = 400
	)
	lb := emutest.NewLoopBreaker(bootBreakThresh)
	for done := 0; done < 30_000_000; done += bootChunkCycles {
		c.Run(bootChunkCycles)
		lb.Check(c.Reg(cpu.PC), c.SetReg)
		if (done/bootChunkCycles)%bootIRQPeriod == 0 {
			c.SetIRQ(5)
			c.Run(bootIRQServiceCost)
			c.SetIRQ(0)
		}
	}

	fmt.Printf("Post boot: PC=%06X\n", c.Reg(cpu.PC))

	// Enable tracing for the operating-loop sweep
	mmio.enabled = true

	const chunkCycles = 50_000
	loops := postCycles / chunkCycles
	for i := 0; i < loops; i++ {
		c.Run(chunkCycles)
		c.SetIRQ(5)
		c.Run(bootIRQServiceCost)
		c.SetIRQ(0)
		if i%4 == 0 {
			c.SetIRQ(6)
			c.Run(bootIRQServiceCost)
			c.SetIRQ(0)
		}
	}

	fmt.Printf("Final PC=%06X  events captured=%d\n", c.Reg(cpu.PC), len(mmio.events))

	// Group reads by (PC, curSel) and writes by (PC, value). Show counts.
	type readKey struct {
		pc, sel uint32
	}
	type writeKey struct {
		pc, val uint32
	}
	readSel := make(map[readKey]map[uint32]int) // map[readKey] -> {val -> count}
	writeStat := make(map[writeKey]int)

	for _, e := range mmio.events {
		switch e.kind {
		case 'R':
			k := readKey{pc: e.pc, sel: uint32(e.curSel)}
			if readSel[k] == nil {
				readSel[k] = make(map[uint32]int)
			}
			readSel[k][e.val]++
		case 'W':
			k := writeKey{pc: e.pc, val: e.val}
			writeStat[k]++
		}
	}

	fmt.Println("\n=== Writes to 0xFFF75C (select port) ===")
	var ws []writeKey
	for k := range writeStat {
		ws = append(ws, k)
	}
	sort.Slice(ws, func(i, j int) bool {
		if writeStat[ws[i]] != writeStat[ws[j]] {
			return writeStat[ws[i]] > writeStat[ws[j]]
		}
		return ws[i].pc < ws[j].pc
	})
	for _, k := range ws {
		fmt.Printf("  PC=%06X writes select=0x%04X ×%d\n", k.pc, k.val, writeStat[k])
	}

	fmt.Println("\n=== Reads from 0xFFF75E (data port; grouped by PC + select) ===")
	var rs []readKey
	for k := range readSel {
		rs = append(rs, k)
	}
	sort.Slice(rs, func(i, j int) bool {
		ci, cj := 0, 0
		for _, c := range readSel[rs[i]] {
			ci += c
		}
		for _, c := range readSel[rs[j]] {
			cj += c
		}
		if ci != cj {
			return ci > cj
		}
		return rs[i].pc < rs[j].pc
	})
	for _, k := range rs {
		total := 0
		for _, c := range readSel[k] {
			total += c
		}
		vals := []uint32{}
		for v := range readSel[k] {
			vals = append(vals, v)
		}
		sort.Slice(vals, func(i, j int) bool { return readSel[k][vals[i]] > readSel[k][vals[j]] })
		valStr := ""
		for i, v := range vals {
			if i >= 4 {
				break
			}
			if valStr != "" {
				valStr += ", "
			}
			valStr += fmt.Sprintf("0x%04X(%d)", v, readSel[k][v])
		}
		fmt.Printf("  PC=%06X sel=0x%02X read ×%d  values: %s\n", k.pc, k.sel, total, valStr)
	}
}
