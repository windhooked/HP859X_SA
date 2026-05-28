// Command abusprobe records every access to the analog-bus ports on the
// HP 8593A's A16 board: writes to 0xFFF75C (the select register), reads
// from 0xFFF75E (the data port — used as the ADC result for the most-
// recent select), and writes to 0xFFF75E (the data port — used as DAC
// data bytes after selects 0x95/0x96/0x97 etc).
//
// Output covers the whole simulated lifetime: boot to operating loop
// plus post-boot run. Events are grouped by (kind, PC, select) so a hot
// loop folds to one line, with the distinct values observed per group.
//
// Usage:
//
//	go run ./cmd/abusprobe/ [post_boot_cycles]   # default 100_000_000
//
// The bus-side responder is the existing pkg/emu/device.HP8593AMMIO so
// the readings reflect the same model used in all other tests.
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

// abusEvent: one access to the analog-bus ports.
type abusEvent struct {
	kind   byte   // 'S' = select write (75C); 'R' = data read (75E); 'D' = data write (75E)
	pc     uint32 // CPU PC at the time
	val    uint32 // value written or read
	curSel uint16 // most-recent select (context for R and D events)
	phase  byte   // 'b' = during boot, 'o' = operating-loop run
}

// abusTracer wraps the real HP8593AMMIO and intercepts 0x75C writes,
// 0x75E reads, and 0x75E writes. It also keeps the "last select" so each
// read/write can be paired with its select for analysis.
type abusTracer struct {
	inner   *device.HP8593AMMIO
	pcFn    func() uint32
	phase   byte // 'b' = boot, 'o' = operating-loop
	curSel  uint16
	events  []abusEvent
	enabled bool
}

func newAbusTracer() *abusTracer {
	return &abusTracer{inner: device.NewHP8593AMMIO(), phase: 'b'}
}

func (a *abusTracer) setPCFunc(fn func() uint32) { a.pcFn = fn }

func (a *abusTracer) Read(addr uint32, sz bus.Size) uint32 {
	v := a.inner.Read(addr, sz)
	if a.enabled && addr == 0x75E && sz == bus.Word {
		ev := abusEvent{kind: 'R', val: v, curSel: a.curSel, phase: a.phase}
		if a.pcFn != nil {
			ev.pc = a.pcFn()
		}
		a.events = append(a.events, ev)
	}
	return v
}

func (a *abusTracer) Write(addr uint32, sz bus.Size, val uint32) {
	a.inner.Write(addr, sz, val)
	if !a.enabled {
		return
	}
	if addr == 0x75C && sz == bus.Word {
		a.curSel = uint16(val)
		ev := abusEvent{kind: 'S', val: val, phase: a.phase}
		if a.pcFn != nil {
			ev.pc = a.pcFn()
		}
		a.events = append(a.events, ev)
		return
	}
	if addr == 0x75E && sz == bus.Word {
		ev := abusEvent{kind: 'D', val: val, curSel: a.curSel, phase: a.phase}
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

	// Enable tracing from cold-reset so the boot-phase analog-bus dance
	// (cal-init, sentinel writes, DAC setup) is captured too.
	mmio.enabled = true
	mmio.phase = 'b'

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

	bootEvents := len(mmio.events)
	fmt.Printf("Post boot: PC=%06X  events during boot=%d\n", c.Reg(cpu.PC), bootEvents)

	mmio.phase = 'o'

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

	fmt.Printf("Final PC=%06X  total events=%d (boot=%d, operating=%d)\n",
		c.Reg(cpu.PC), len(mmio.events), bootEvents, len(mmio.events)-bootEvents)

	// Group select writes by (phase, PC, value), data reads by (phase, PC, sel),
	// data writes by (phase, PC, sel).
	type selKey struct {
		phase   byte
		pc, val uint32
	}
	type dataKey struct {
		phase   byte
		pc, sel uint32
	}
	selStat := make(map[selKey]int)
	readStat := make(map[dataKey]map[uint32]int)
	writeStat := make(map[dataKey]map[uint32]int)

	for _, e := range mmio.events {
		switch e.kind {
		case 'S':
			selStat[selKey{phase: e.phase, pc: e.pc, val: e.val}]++
		case 'R':
			k := dataKey{phase: e.phase, pc: e.pc, sel: uint32(e.curSel)}
			if readStat[k] == nil {
				readStat[k] = make(map[uint32]int)
			}
			readStat[k][e.val]++
		case 'D':
			k := dataKey{phase: e.phase, pc: e.pc, sel: uint32(e.curSel)}
			if writeStat[k] == nil {
				writeStat[k] = make(map[uint32]int)
			}
			writeStat[k][e.val]++
		}
	}

	// Helpers to render sorted summaries per phase.
	phaseName := func(p byte) string {
		if p == 'b' {
			return "BOOT"
		}
		return "OPERATING-LOOP"
	}

	dumpSelects := func(p byte) {
		fmt.Printf("\n=== [%s] Writes to 0xFFF75C (select port) ===\n", phaseName(p))
		var ks []selKey
		for k := range selStat {
			if k.phase == p {
				ks = append(ks, k)
			}
		}
		sort.Slice(ks, func(i, j int) bool {
			if selStat[ks[i]] != selStat[ks[j]] {
				return selStat[ks[i]] > selStat[ks[j]]
			}
			return ks[i].pc < ks[j].pc
		})
		for _, k := range ks {
			fmt.Printf("  PC=%06X select=0x%04X ×%d\n", k.pc, k.val, selStat[k])
		}
	}

	dumpData := func(stat map[dataKey]map[uint32]int, p byte, label string) {
		fmt.Printf("\n=== [%s] %s from 0xFFF75E (grouped by PC + select) ===\n",
			phaseName(p), label)
		var ks []dataKey
		for k := range stat {
			if k.phase == p {
				ks = append(ks, k)
			}
		}
		sort.Slice(ks, func(i, j int) bool {
			ci, cj := 0, 0
			for _, c := range stat[ks[i]] {
				ci += c
			}
			for _, c := range stat[ks[j]] {
				cj += c
			}
			if ci != cj {
				return ci > cj
			}
			return ks[i].pc < ks[j].pc
		})
		for _, k := range ks {
			total := 0
			for _, c := range stat[k] {
				total += c
			}
			vals := []uint32{}
			for v := range stat[k] {
				vals = append(vals, v)
			}
			sort.Slice(vals, func(i, j int) bool { return stat[k][vals[i]] > stat[k][vals[j]] })
			valStr := ""
			for i, v := range vals {
				if i >= 6 {
					valStr += fmt.Sprintf(", +%d more", len(vals)-i)
					break
				}
				if valStr != "" {
					valStr += ", "
				}
				valStr += fmt.Sprintf("0x%04X(%d)", v, stat[k][v])
			}
			fmt.Printf("  PC=%06X sel=0x%02X ×%d  values: %s\n", k.pc, k.sel, total, valStr)
		}
	}

	dumpSelects('b')
	dumpData(writeStat, 'b', "Writes")
	dumpData(readStat, 'b', "Reads")
	dumpSelects('o')
	dumpData(writeStat, 'o', "Writes")
	dumpData(readStat, 'o', "Reads")
}
