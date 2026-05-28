// Command layer2 wraps the main RAM with a tracer that records every
// write to the LAYER 2 gating RAM locations during a boot + post-boot
// operating loop. Output: a per-(addr, pc) tally so we can see which
// firmware code paths NATURALLY set/clear each gate bit and at what
// cadence — the data needed to plan a clean LAYER 2 fix.
//
// Watched addresses:
//
//	0xFFB010  sweep status snap (target of move.w f300, b010)
//	0xFFB072  upstream control bits 2+5 (fcn.40720 gate)
//	0xFFB0AB  enables fcn.A250 which sets b0ce.11
//	0xFFB0CE  state/sweep flags (bit 11 = key path)
//	0xFFB1E0  mode bits (bit 9 = key pending)
//	0xFFB1F8  loop-exit flags (bits 11+12 = 0x1800 needed)
//	0xFFB1F9  service-request flags (bit 3 = fcn.40720 gate)
//	0xFFBEFA  IRQ flags (bit 10 should be clear, bit 13 = sweep done)
//	0xFFBEFD  dispatcher state (bit 7 = path B route)
//	0xFFBEFE  dispatcher mode (bit 6 = 0x1ED0 dispatch)
//	0xFF9AFB  operating-mode (bit 2 = 0x18E6E fall-through)
//
// Usage:
//
//	go run ./cmd/layer2/ [post_boot_chunks]   # default 200 × 50K = 10M cycles
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

// watched is the LAYER 2 gating set.
var watched = []struct {
	addr uint32
	name string
}{
	{0xFFB010, "b010 sweep-status snap"},
	{0xFFB072, "b072 upstream gate"},
	{0xFFB0AB, "b0ab fcn.A250 enable"},
	{0xFFB0CE, "b0ce state (bit 11 deep)"},
	{0xFFB1E0, "b1e0 mode (bit 9 key)"},
	{0xFFB1F8, "b1f8 loop-exit (11+12)"},
	{0xFFB1F9, "b1f9 srvc (bit 3 gate)"},
	{0xFFBEFA, "befa irq (10 clear, 13)"},
	{0xFFBEFD, "befd dispatch (bit 7)"},
	{0xFFBEFE, "befe dispatch (bit 6)"},
	{0xFF9AFB, "9afb op-mode (bit 2)"},
}

type event struct {
	addr uint32
	pc   uint32
	val  uint32
	sz   bus.Size
}

type ramTracer struct {
	inner   *bus.RAM
	base    uint32
	pcFn    func() uint32
	enabled bool
	count   map[event]int
}

func newRAMTracer(inner *bus.RAM, base uint32) *ramTracer {
	return &ramTracer{inner: inner, base: base, count: make(map[event]int)}
}

func (r *ramTracer) Read(addr uint32, sz bus.Size) uint32 {
	return r.inner.Read(addr, sz)
}

func (r *ramTracer) Write(addr uint32, sz bus.Size, val uint32) {
	r.inner.Write(addr, sz, val)
	if !r.enabled {
		return
	}
	abs := r.base + addr
	for _, w := range watched {
		if abs >= w.addr-1 && abs <= w.addr+3 {
			ev := event{addr: abs, val: val & 0xFFFFFF, sz: sz}
			if r.pcFn != nil {
				ev.pc = r.pcFn()
			}
			r.count[ev]++
			break
		}
	}
}

func main() {
	chunks := 200
	if len(os.Args) > 1 {
		if n, err := strconv.Atoi(os.Args[1]); err == nil && n > 0 {
			chunks = n
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
	mmio := device.NewHP8593AMMIO()

	b.Map(0x000000, uint32(len(rom)), "ROM", bus.NewROM(rom))
	b.Map(device.CalNVRAMBase, device.CalNVRAMSize, "CalNVRAM", calNVRAM)
	b.Map(0x2FC000, 0x004000, "CalRAM", bus.NewRAM(0x004000))
	b.Map(0xEF8000, 0x000100, "PIT", bus.NewRAM(0x000100))
	b.Map(device.FrontPanelBase, device.FrontPanelSize, "FrontPanel", device.NewFrontPanel())
	b.Map(0xFEC000, 0x004000, "TestRAM", bus.NewRAM(0x004000))

	const ramBase = uint32(0xFF0000)
	const ramSize = uint32(0x00F000)
	tracer := newRAMTracer(bus.NewRAM(ramSize), ramBase)
	b.Map(ramBase, ramSize, "RAM", tracer)
	b.Map(device.MMIOBase, device.MMIOSize, "MMIO", mmio)

	c, err := musashi.New(b)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	tracer.pcFn = func() uint32 { return c.Reg(cpu.PC) }

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

	// Dump pre-tracer state.
	fmt.Println("Post-boot LAYER 2 gate state:")
	for _, w := range watched {
		v := b.Read(w.addr, bus.Word)
		fmt.Printf("  %-30s [%08X] = %04X\n", w.name, w.addr, v)
	}

	// Enable tracing for the operating-loop sweep.
	tracer.enabled = true

	const chunkCycles = 50_000
	for i := 0; i < chunks; i++ {
		c.Run(chunkCycles)
		c.SetIRQ(5)
		c.Run(bootIRQServiceCost)
		c.SetIRQ(0)
		if i%4 == 0 {
			c.SetIRQ(6)
			c.Run(bootIRQServiceCost)
			c.SetIRQ(0)
		}
		if i%8 == 4 {
			c.SetIRQ(4)
			c.Run(bootIRQServiceCost)
			c.SetIRQ(0)
		}
	}

	fmt.Printf("\nAfter %d chunks (%d cycles total) of operating loop:\n",
		chunks, chunks*chunkCycles)
	for _, w := range watched {
		v := b.Read(w.addr, bus.Word)
		fmt.Printf("  %-30s [%08X] = %04X\n", w.name, w.addr, v)
	}

	// Per-address: list distinct (pc, val) pairs by total count.
	fmt.Println("\nWrite events per watched address (top 5 per addr):")
	byAddr := make(map[uint32][]event)
	for ev, n := range tracer.count {
		_ = n
		byAddr[ev.addr] = append(byAddr[ev.addr], ev)
	}
	for _, w := range watched {
		evs := byAddr[w.addr]
		if len(evs) == 0 {
			fmt.Printf("\n  [%08X] %s — NO WRITES (naturally inert in operating loop)\n",
				w.addr, w.name)
			continue
		}
		sort.Slice(evs, func(i, j int) bool {
			return tracer.count[evs[i]] > tracer.count[evs[j]]
		})
		fmt.Printf("\n  [%08X] %s — %d distinct writes:\n", w.addr, w.name, len(evs))
		for i, ev := range evs {
			if i >= 5 {
				fmt.Printf("    +%d more\n", len(evs)-5)
				break
			}
			fmt.Printf("    PC=%06X  val=%06X sz=%d  ×%d\n",
				ev.pc, ev.val, ev.sz, tracer.count[ev])
		}
	}
}
