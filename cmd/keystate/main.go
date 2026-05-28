// Command keystate boots the HP 8593A to its operating loop and traces
// every write to the Rev L key-dispatch state machine — RAM[0xFFBF03]
// (event flag), RAM[0xFFBF0A] (pending-function pointer), RAM[0xFFBC67]
// (IRQ3-set key-available flag). Goal: identify the outer event tick that
// invokes fcn.1B40 (the dispatch router that bridges to the key consumer
// at 0x148 -> 0x18568), and detect whether the firmware ever calls it
// during the operating loop.
//
// Strategy: wrap the main RAM with a tracing device that records every
// write within the watched address range and stamps it with the current
// PC. After boot, the operating loop is run while injecting IRQ5 ticks;
// optionally an IRQ3 key event is injected partway through. The recorded
// writes are dumped at the end, grouped by (addr, PC, value) so duplicate
// writes from a hot loop collapse to one line.
//
// Usage:
//
//	go run ./cmd/keystate/ [post_boot_cycles]   # default 30_000_000
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

// watchedAddrs lists the absolute RAM addresses we trace. Each is one byte
// or longword inside the main RAM region (0xFF0000..0xFFEFFF).
var watchedAddrs = []uint32{
	0xFFBF03, // event flag (set 0x81 at PC 0x731E; cleared at 0x1BA4)
	0xFFBF0A, // pending-function pointer (cleared at 0x1BA8)
	0xFFBC67, // key-available flag bit 0 (set by IRQ3 at 0x002B26)
}

// watchedRange covers the watched addresses with a few bytes of slack —
// the address range checked in the hot Write() path. Keep the range tight
// to avoid logging noise.
const (
	watchLo = 0xFFBC67
	watchHi = 0xFFBF0F
)

// traceEvent records one observed write. Aggregated by key (addr, pc, val).
type traceEvent struct {
	addr uint32
	pc   uint32
	val  uint32
	sz   bus.Size
}

// ramTracer wraps a bus.RAM and records every write that falls in the
// watched range. The trace channel is unbuffered until the run finishes;
// to keep the hot-path overhead minimal we batch into a slice protected by
// a single fast path test.
type ramTracer struct {
	inner    *bus.RAM
	base     uint32
	pcFn     func() uint32
	events   []traceEvent
	seen     map[traceEvent]int // dedupe counter
	enabled  bool
}

func newRAMTracer(inner *bus.RAM, base uint32) *ramTracer {
	return &ramTracer{
		inner: inner,
		base:  base,
		seen:  make(map[traceEvent]int),
	}
}

func (r *ramTracer) setPCFunc(fn func() uint32) { r.pcFn = fn }

func (r *ramTracer) Read(addr uint32, sz bus.Size) uint32 {
	return r.inner.Read(addr, sz)
}

func (r *ramTracer) Write(addr uint32, sz bus.Size, val uint32) {
	r.inner.Write(addr, sz, val)
	abs := r.base + addr
	if !r.enabled || abs < watchLo || abs > watchHi {
		return
	}
	// Mask val to the access size so a long write of 0x000192C8 is recorded
	// as that exact value, not extended.
	switch sz {
	case bus.Byte:
		val &= 0xFF
	case bus.Word:
		val &= 0xFFFF
	}
	ev := traceEvent{addr: abs, val: val, sz: sz}
	if r.pcFn != nil {
		ev.pc = r.pcFn()
	}
	r.seen[ev]++
	if r.seen[ev] == 1 {
		r.events = append(r.events, ev)
	}
}

func main() {
	postCycles := 30_000_000
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

	// Build the bus manually so we can swap in the tracing RAM in place of
	// the standard RAM region. Everything else mirrors machine.New8593A.
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
	tracer.setPCFunc(func() uint32 { return c.Reg(cpu.PC) })

	c.Reset()

	// Boot using the same parameters as machine.BootToOperating, but inline
	// so we can keep the tracer attached and switch its enable flag on
	// AFTER boot — boot writes are noisy and not what we're investigating.
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

	fmt.Printf("Post boot: PC=%06X  bf03=%02X  bf0a=%08X  bc67=%02X\n",
		c.Reg(cpu.PC),
		b.Read(0xFFBF03, bus.Byte),
		b.Read(0xFFBF0A, bus.Long),
		b.Read(0xFFBC67, bus.Byte))

	// Enable tracing and run the operating loop with periodic IRQ5 + IRQ6.
	// IRQ6 is the sweep-complete handler at ROM 0x4088 — without it the
	// firmware never advances past the sweep service path.
	tracer.enabled = true

	const chunkCycles = 50_000
	loops := postCycles / chunkCycles
	if loops < 1 {
		loops = 1
	}
	injectIRQ3At := loops / 2

	for i := 0; i < loops; i++ {
		c.Run(chunkCycles)
		c.SetIRQ(5)
		c.Run(bootIRQServiceCost)
		c.SetIRQ(0)
		// Every 4th chunk also try IRQ6 (sweep complete).
		if i%4 == 0 {
			c.SetIRQ(6)
			c.Run(bootIRQServiceCost)
			c.SetIRQ(0)
		}
		// Every 8th chunk try IRQ4 (HP-IB). fcn.1D58 — the entry into
		// the dispatch path that calls fcn.1B40 (and the key consumer)
		// — is invoked from multiple sites in the 0x26xx region, which
		// is the IRQ4 handler body. Without IRQ4 ticks the dispatcher
		// never runs, regardless of bf03/bf0a state.
		if i%8 == 4 {
			c.SetIRQ(4)
			c.Run(bootIRQServiceCost)
			c.SetIRQ(0)
		}
		// Halfway through: inject a key press, then directly force the
		// dispatch chain by clearing bf0a (the pending function-pointer
		// gate). If the consumer is reached the IRQ3 handler at 0x18568
		// runs and clears bc67 bit 0. This tests the chain end-to-end
		// without needing the (currently unknown) natural event tick.
		if i == injectIRQ3At {
			c.SetIRQ(3)
			c.Run(bootIRQServiceCost)
			c.SetIRQ(0)
			fmt.Printf("\n>>> IRQ3 injected at i=%d  bc67=%02X  bf0a=%08X <<<\n",
				i, b.Read(0xFFBC67, bus.Byte), b.Read(0xFFBF0A, bus.Long))

			// Forcibly clear bf0a — bypasses the "perpetual fcn.192C8"
			// dispatch. Next call to fcn.1B40 (if any) will then take
			// the bf03==0 && bf0a==0 path -> JMP $148 -> key consumer.
			b.Write(0xFFBF0A, bus.Long, 0)
			fmt.Printf(">>> bf0a forced to 0; continuing run <<<\n\n")
		}
	}

	fmt.Printf("\nFinal: PC=%06X  bf03=%02X  bf0a=%08X  bc67=%02X\n",
		c.Reg(cpu.PC),
		b.Read(0xFFBF03, bus.Byte),
		b.Read(0xFFBF0A, bus.Long),
		b.Read(0xFFBC67, bus.Byte))

	// Dump captured writes. Sort by addr, then PC.
	sort.Slice(tracer.events, func(i, j int) bool {
		a, b := tracer.events[i], tracer.events[j]
		if a.addr != b.addr {
			return a.addr < b.addr
		}
		return a.pc < b.pc
	})

	fmt.Printf("\n=== Watched-RAM write events (%d distinct, %d total writes seen) ===\n",
		len(tracer.events), sumCounts(tracer.seen))
	if len(tracer.events) == 0 {
		fmt.Println("(no writes recorded in the watched range during operating loop)")
		return
	}
	for _, e := range tracer.events {
		cnt := tracer.seen[e]
		fmt.Printf("  addr=%06X  size=%d  val=%-8X  at PC=%06X   ×%d\n",
			e.addr, int(e.sz), e.val, e.pc, cnt)
	}
}

func sumCounts(m map[traceEvent]int) int {
	s := 0
	for _, v := range m {
		s += v
	}
	return s
}
