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
		pollTop  = 0x05E5FA // write select 0x9A
		readDone = 0x05E600 // just read 0xFFF75E into $9492
		cmpPC    = 0x05E60E // cmp.b (9,A6), D6
		setMatch = 0x05E614 // result = -1 (match)
		pollExit = 0x05E630 // poll returns
		caller   = 0x05E64C // back in fcn.5E63C after first poll
	)
	var topHits, exitHits, callerHits, matchSet, logged int
	for i := 0; i < steps; i++ {
		pc := m.CPU.Reg(cpu.PC)
		switch pc {
		case pollTop:
			topHits++
		case pollExit:
			exitHits++
		case setMatch:
			matchSet++
		case caller:
			callerHits++
		}
		if pc == cmpPC {
			d6 := m.CPU.Reg(cpu.D6) & 0xFF
			a6 := m.CPU.Reg(cpu.A6)
			expected := m.Bus.Read(a6+9, bus.Byte) & 0xFF
			testByte := m.Bus.Read(a6-1, bus.Byte) & 0xFF // (-1,A6) pre-mask test byte
			readLow := m.Bus.Read(0xFF9493, bus.Byte) & 0xFF
			rawRead := m.Bus.Read(0xFF9492, bus.Word) & 0xFFFF
			match := d6 == expected
			// Log the first 8 and any time the read is non-zero (periodic 0x0006).
			if logged < 8 || rawRead != 0 {
				fmt.Printf("  cmp#%-3d read(f75e)=%#04x test(-1,A6)=%#02x mask&read_low=%#02x D6=%#02x expected=%#02x %s\n",
					logged, rawRead, testByte, readLow, d6, expected, map[bool]string{true: "MATCH", false: "no"}[match])
			}
			logged++
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
	fmt.Printf("poll-top  0x5E5FA hits : %d\n", topHits)
	fmt.Printf("match-set 0x5E614 hits : %d  (read satisfied (mask&x)==expected)\n", matchSet)
	fmt.Printf("poll-exit 0x5E630 hits : %d\n", exitHits)
	fmt.Printf("caller    0x5E64C hits : %d  (progressed past the first poll)\n", callerHits)
	fmt.Printf("final PC = %#06x\n", m.CPU.Reg(cpu.PC))
}

func main() {
	bootCycles := flag.Int("boot", 60_000_000, "cycles to boot to operating")
	runCycles := flag.Int("run", 300_000_000, "cycles to run the natural operating loop")
	noKey := flag.Bool("nokey", false, "do not inject a key (isolate the boot stall)")
	trace := flag.Int("trace", 0, "if >0: single-step N instructions from post-boot, tracing the analog poll instead of the bulk run")
	flag.Parse()

	img, err := romloader.LoadDir("hp8593a_eeproms")
	if err != nil {
		panic(err)
	}
	m, err := machine.New8593A(img)
	if err != nil {
		panic(err)
	}
	m.CPU.Reset()
	m.BootToOperating(*bootCycles)

	postBootPC := m.CPU.Reg(cpu.PC)
	fmt.Printf("post-boot PC = %#06x\n", postBootPC)
	fmt.Printf("post-boot bc67 = %#02x  (bit0=key-flag)\n", byte(m.Bus.Read(0xFFBC67, bus.Byte)))

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
