// Command naturalkey — Path B (long-run integration) probe for the Rev L
// dispatch blocker (docs/DRIVETICK_BLOCKER.md).
//
// Unlike DriveOperatingTick (which FORCES PC into the deep block and pre-arms
// gate cells), this probe runs the operating loop the way real hardware would:
//
//   - boot naturally to the operating loop,
//   - inject a real front-panel key event via the FrontPanel device,
//   - then pump a NATURAL run loop — periodic IRQ5 timer ticks, IRQ3 delivered
//     whenever the device has a key pending, and the boot LoopBreaker — with NO
//     forced PC and NO forced bc67.1 / b072.14 gate bits.
//
// It answers the central Path B question empirically: does the natural
// operating loop iterate through fcn.18568 at all (and thus call the
// matrix-read routine 0x3AB52 → FrontPanel.Consumed()), or is the post-boot
// CPU parked in the analog-bus poll loop at 0x5E5FA forever?
//
// Output: a PC-region histogram over the whole post-boot run, landmark
// visit counts, a bc67 (key flag) timeline, and whether the firmware ever
// consumed the injected key.
package main

import (
	"flag"
	"fmt"
	"sort"

	"github.com/windhooked/HP859X_SA/internal/emutest"
	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
	"github.com/windhooked/HP859X_SA/pkg/emu/machine"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

const (
	chunkCycles      = 2000 // cycles per Run() call (matches machine.bootChunkCycles)
	irqServiceCost   = 400
	irq5EveryNChunks = 5
)

// landmark PC ranges we care about (inclusive lo..hi).
type landmark struct {
	name   string
	lo, hi uint32
}

var landmarks = []landmark{
	{"operating-body fcn.18568", 0x018568, 0x018A88},
	{"  key-flag bclr 0x18F42", 0x018F42, 0x018F42},
	{"  dispatch-gate1 0x18F5E", 0x018F5E, 0x018F5E},
	{"  per-key dispatch 0x18F84", 0x018F84, 0x018F84},
	{"matrix-read 0x3AB52", 0x03AB52, 0x03AC00},
	{"fp-matrix slot 0x59D2A", 0x059D2A, 0x059E00},
	{"hpib-parser 0x58C2E", 0x058C2E, 0x058D00},
	{"analog-poll 0x5E5FA", 0x05E5FA, 0x05E620},
	{"analog-poll-EXIT 0x5E630", 0x05E630, 0x05E63A},
	{"compare-helper fcn.4824", 0x004824, 0x004900},
	{"deepchain fcn.568F6", 0x0568F6, 0x056A00},
	{"deepchain fcn.11DF4", 0x011DF4, 0x011F00},
}

func regionBucket(pc uint32) uint32 { return pc &^ 0x3FF } // 1 KB buckets

// runTrace single-steps from the post-boot state, tracing the analog poll
// loop: counts loop-top (0x5E5FA) and exit (0x5E630) visits, logs the first
// few read values + compare results, and reports whether/when the poll matches.
func runTrace(m *machine.Machine, steps int) {
	const (
		eqPollTop = 0x05E6FC // write select 0x9A (==0x06 init poll top)
		eqCmp     = 0x05E708 // cmpi.b #6, $9493
		eqExit    = 0x05E71E // matched ==0x06, fall through
	)
	var topHits, exitHits, logged int
	valdist := map[uint16]int{}
	for i := 0; i < steps; i++ {
		pc := m.CPU.Reg(cpu.PC)
		switch pc {
		case eqPollTop:
			topHits++
		case eqExit:
			exitHits++
		}
		if pc == eqCmp {
			lowByte := uint16(m.Bus.Read(0xFF9493, bus.Byte) & 0xFF)
			valdist[lowByte]++
			if logged < 12 {
				fmt.Printf("  eqcmp#%-2d $9493=%#02x %s\n", logged, lowByte,
					map[bool]string{true: "==0x06 MATCH", false: "no"}[lowByte == 0x06])
				logged++
			}
		}
		if err := m.CPU.Step(); err != nil {
			fmt.Printf("step error at %#06x: %v\n", pc, err)
			break
		}
		// Advance the IRQ5 timer occasionally so timeout windows can elapse.
		if i%4000 == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Run(irqServiceCost)
			m.CPU.SetIRQ(0)
		}
	}
	fmt.Printf("\n=== trace summary (%d steps) ===\n", steps)
	fmt.Printf("==0x06 poll-top 0x5E6FC hits : %d\n", topHits)
	fmt.Printf("==0x06 poll-exit 0x5E71E hits : %d  (matched, progressed)\n", exitHits)
	fmt.Printf("$9493 value distribution at the ==0x06 cmp: %v\n", valdist)
	fmt.Printf("final PC = %#06x\n", m.CPU.Reg(cpu.PC))
}

// saneePC reports whether pc is in a region the firmware legitimately
// executes from: ROM (0..0xFFFFF) or RAM/MMIO (0xFF0000..0xFFFFFF).
func sanePC(pc uint32) bool {
	return pc < 0x100000 || (pc >= 0xFF0000 && pc <= 0xFFFFFF)
}

// runDerailScan boots in chunks (mirroring Machine.BootToOperating's cadence)
// while watching for the PC to jump to a wild address. It keeps a ring of the
// recent distinct PCs so the last sane location before the derail is visible.
func runDerailScan(m *machine.Machine, maxCycles int) {
	m.CPU.Reset()
	lb := emutest.NewLoopBreaker(50)
	const ringN = 32
	ring := make([]uint32, 0, ringN)
	push := func(pc uint32) {
		if len(ring) > 0 && ring[len(ring)-1] == pc {
			return // collapse runs of the same PC
		}
		if len(ring) == ringN {
			ring = ring[1:]
		}
		ring = append(ring, pc)
	}
	// Phase 1: chunked run up to a margin before the known derail.
	const stepMargin = 3_000_000
	phase1 := maxCycles - stepMargin
	if phase1 < 0 {
		phase1 = 0
	}
	for done := 0; done < phase1; done += chunkCycles {
		m.CPU.Run(chunkCycles)
		pc := m.CPU.Reg(cpu.PC)
		push(pc)
		if !sanePC(pc) {
			fmt.Printf("DERAIL (chunk) at ~%d cycles: PC=%#08x\n", done, pc)
			for _, p := range ring {
				fmt.Printf("  %#08x\n", p)
			}
			return
		}
		lb.Check(pc, m.CPU.SetReg)
		if (done/chunkCycles)%irq5EveryNChunks == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Run(irqServiceCost)
			m.CPU.SetIRQ(0)
		}
	}
	// Phase 2: single-step to catch the exact derailing instruction.
	fmt.Printf("phase 2: single-stepping from ~%d cycles (PC=%#06x)\n", phase1, m.CPU.Reg(cpu.PC))
	type disp struct{ recPtr, token, handler uint32 }
	var dtrail []disp
	prev := m.CPU.Reg(cpu.PC)
	for i := 0; i < 4_000_000; i++ {
		if err := m.CPU.Step(); err != nil {
			fmt.Printf("step error at %#06x: %v\n", prev, err)
			return
		}
		pc := m.CPU.Reg(cpu.PC)
		if pc == 0x34C94 { // jsr (A1): record the DLP token dispatch
			a6 := m.CPU.Reg(cpu.A6)
			recPtr := m.Bus.Read(a6-0x1e, bus.Long)
			dtrail = append(dtrail, disp{recPtr, m.Bus.Read(recPtr, bus.Word), m.CPU.Reg(cpu.A1)})
			if len(dtrail) > 24 {
				dtrail = dtrail[1:]
			}
		}
		if !sanePC(pc) {
			fmt.Printf("\nDERAIL: %#06x -> %#08x\n", prev, pc)
			fmt.Printf("DLP dispatch trail (recPtr, token, handler) — last %d:\n", len(dtrail))
			for _, d := range dtrail {
				fmt.Printf("  recPtr=%#07x token=%#05x handler=%#08x\n", d.recPtr, d.token, d.handler)
			}
			fmt.Printf("regs: D0=%#x D1=%#x D6=%#x A0=%#x A1=%#x A4=%#x A6=%#x A7=%#x\n",
				m.CPU.Reg(cpu.D0), m.CPU.Reg(cpu.D1), m.CPU.Reg(cpu.D6),
				m.CPU.Reg(cpu.A0), m.CPU.Reg(cpu.A1), m.CPU.Reg(cpu.A4),
				m.CPU.Reg(cpu.A6), m.CPU.Reg(cpu.A7))
			fmt.Println("DLP runtime state:")
			fmt.Printf("  $bb54 (symbol table base) = %#x\n", m.Bus.Read(0xFFBB54, bus.Long))
			fmt.Printf("  fg ring $a630=%#x $a632=%#x $a634=%#x\n",
				m.Bus.Read(0xFFA630, bus.Word), m.Bus.Read(0xFFA632, bus.Word), m.Bus.Read(0xFFA634, bus.Word))
			fmt.Printf("  fg DLP state block $a61c[0..7] = ")
			for k := uint32(0); k < 8; k++ {
				fmt.Printf("%#x ", m.Bus.Read(0xFFA61C+k*2, bus.Word))
			}
			fmt.Printf("\n  $a7da=%#x\n", m.Bus.Read(0xFFA7DA, bus.Word))
			fmt.Printf("  $a02 (DLP record base) = %#x\n", m.Bus.Read(0xFFA02, bus.Long))
			fmt.Printf("  char-ring: base$a62c=%#x size$a62a=%#x head$a630=%#x tail$a632=%#x\n",
				m.Bus.Read(0xFFA62C, bus.Long), m.Bus.Read(0xFFA62A, bus.Word),
				m.Bus.Read(0xFFA630, bus.Word), m.Bus.Read(0xFFA632, bus.Word))
			return
		}
		prev = pc
		if i%4000 == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Run(irqServiceCost)
			m.CPU.SetIRQ(0)
		}
	}
	fmt.Printf("no derail; final PC=%#06x\n", m.CPU.Reg(cpu.PC))
}

func main() {
	bootCycles := flag.Int("boot", 60_000_000, "cycles to boot to operating")
	runCycles := flag.Int("run", 300_000_000, "cycles to run the natural operating loop")
	noKey := flag.Bool("nokey", false, "do not inject a key (isolate the boot stall)")
	trace := flag.Int("trace", 0, "if >0: single-step N instructions from post-boot, tracing the analog poll instead of the bulk run")
	derail := flag.Bool("derail", false, "boot in chunks and report the last sane PC before a wild jump")
	flag.Parse()

	img, err := romloader.LoadDir("hp8593a_eeproms")
	if err != nil {
		panic(err)
	}
	m, err := machine.New8593A(img)
	if err != nil {
		panic(err)
	}

	if *derail {
		runDerailScan(m, *bootCycles)
		return
	}

	m.CPU.Reset()
	m.BootToOperating(*bootCycles)

	postBootPC := m.CPU.Reg(cpu.PC)
	fmt.Printf("post-boot PC = %#06x\n", postBootPC)
	fmt.Printf("post-boot bc67 = %#02x  (bit0=key-flag)\n", byte(m.Bus.Read(0xFFBC67, bus.Byte)))
	fmt.Printf("post-boot $bb54 (DLP symtab) = %#x  $bb4e (heap ptr) = %#x  $bff1 = %#x\n",
		m.Bus.Read(0xFFBB54, bus.Long), m.Bus.Read(0xFFBB4E, bus.Long), m.Bus.Read(0xFFBFF1, bus.Byte))

	// Inject a real key matrix. Bit (byte 0, bit 0) — any single key; we only
	// care whether the firmware's natural chain reads + dispatches it.
	if !*noKey {
		m.FrontPanel.SetBit(0, 0)
		fmt.Printf("injected key matrix (byte0 bit0); FrontPanel.Pending()=%v\n\n", m.FrontPanel.Pending())
	} else {
		fmt.Printf("(-nokey) isolating boot stall; no key injected\n\n")
	}

	if *trace > 0 {
		runTrace(m, *trace)
		return
	}

	timerStart := m.Bus.Read(0xFFBF12, bus.Long)
	var timerMin, timerMax uint32 = timerStart, timerStart

	hist := map[uint32]int{}
	lmCount := make([]int, len(landmarks))
	var bc67Set, bc67Clear int
	prevBC67 := byte(m.Bus.Read(0xFFBC67, bus.Byte))
	var bc67Transitions []string
	consumedAt := -1
	irq3Deliveries := 0

	lb := emutest.NewLoopBreaker(50)
	samples := 0
	for done := 0; done < *runCycles; done += chunkCycles {
		// Deliver IRQ3 while the device has a key pending (the device cannot
		// assert it itself). This is the natural front-panel interrupt.
		if m.FrontPanel.Pending() {
			m.CPU.SetIRQ(3)
			m.CPU.Run(irqServiceCost)
			m.CPU.SetIRQ(0)
			irq3Deliveries++
		}

		m.CPU.Run(chunkCycles)
		pc := m.CPU.Reg(cpu.PC)
		lb.Check(pc, m.CPU.SetReg)

		samples++
		hist[regionBucket(pc)]++
		if t := m.Bus.Read(0xFFBF12, bus.Long); t < timerMin {
			timerMin = t
		} else if t > timerMax {
			timerMax = t
		}
		for i, lm := range landmarks {
			if pc >= lm.lo && pc <= lm.hi {
				lmCount[i]++
			}
		}

		bc := byte(m.Bus.Read(0xFFBC67, bus.Byte))
		if bc != prevBC67 {
			if len(bc67Transitions) < 40 {
				bc67Transitions = append(bc67Transitions,
					fmt.Sprintf("  @%3dM cyc: bc67 %#02x -> %#02x (pc=%#06x)", (done)/1_000_000, prevBC67, bc, pc))
			}
			if bc&0x01 != 0 && prevBC67&0x01 == 0 {
				bc67Set++
			}
			if bc&0x01 == 0 && prevBC67&0x01 != 0 {
				bc67Clear++
			}
			prevBC67 = bc
		}

		if consumedAt < 0 && m.FrontPanel.Consumed() {
			consumedAt = done
		}

		if (done/chunkCycles)%irq5EveryNChunks == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Run(irqServiceCost)
			m.CPU.SetIRQ(0)
		}
	}

	fmt.Printf("=== run summary (%d samples over %dM cycles) ===\n", samples, *runCycles/1_000_000)
	fmt.Printf("final PC          = %#06x\n", m.CPU.Reg(cpu.PC))
	fmt.Printf("IRQ3 deliveries   = %d\n", irq3Deliveries)
	fmt.Printf("FrontPanel.Consumed = %v", m.FrontPanel.Consumed())
	if consumedAt >= 0 {
		fmt.Printf("  (first at %dM cycles)\n", consumedAt/1_000_000)
	} else {
		fmt.Printf("  (NEVER — firmware never read the matrix)\n")
	}
	fmt.Printf("bc67 bit0 set %d times, cleared %d times\n", bc67Set, bc67Clear)
	fmt.Printf("$bf12 timer: start=%d min=%d max=%d (advance=%d)\n\n",
		timerStart, timerMin, timerMax, timerMax-timerStart)

	fmt.Println("=== landmark visits (chunk-boundary samples) ===")
	for i, lm := range landmarks {
		flag := ""
		if lmCount[i] > 0 {
			flag = "  <-- VISITED"
		}
		fmt.Printf("  %-28s %#06x..%#06x : %6d%s\n", lm.name, lm.lo, lm.hi, lmCount[i], flag)
	}

	fmt.Println("\n=== top 15 PC regions (1 KB buckets) ===")
	type kv struct {
		k uint32
		v int
	}
	var sorted []kv
	for k, v := range hist {
		sorted = append(sorted, kv{k, v})
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].v > sorted[j].v })
	for i := 0; i < len(sorted) && i < 15; i++ {
		pct := 100.0 * float64(sorted[i].v) / float64(samples)
		fmt.Printf("  %#06x : %6d (%4.1f%%)\n", sorted[i].k, sorted[i].v, pct)
	}

	if len(bc67Transitions) > 0 {
		fmt.Println("\n=== bc67 transitions (first 40) ===")
		for _, t := range bc67Transitions {
			fmt.Println(t)
		}
	}
}
