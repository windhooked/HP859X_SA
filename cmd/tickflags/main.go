// Command tickflags boots the HP 8593A to the operating loop and dumps
// every RAM state flag that fcn.18568 (the operating tick) tests in its
// early branches, so a "tick driver" can see what's naturally set and
// what needs pre-arming to drive the function down the deep path that
// reaches the bclr-bc67 at PC 0x18F42 (key handling) and the
// sweep-done bclr at fcn.17346 (trace render).
//
// Usage:
//
//	go run ./cmd/tickflags/ [boot_cycles]   # default 30M
//
// The dump output lists each tested flag as:
//
//	NAME    addr=...   value=...   bit-of-interest=...   exit-if=...
//
// where "exit-if" describes the early-exit condition the flag controls
// in the operating tick. Pre-arming RAM[addr] so the bit takes the
// opposite value is the basic tick-driver primitive.
package main

import (
	"fmt"
	"os"
	"sort"
	"strconv"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
	"github.com/windhooked/HP859X_SA/pkg/emu/machine"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

// flagSpec describes one of the operating-tick's early-exit checks.
type flagSpec struct {
	name       string
	addr       uint32 // absolute RAM address
	width      bus.Size
	bit        int    // bit-of-interest (-1 = whole word/byte compared)
	cmp        uint32 // comparison value (when bit==-1)
	exitDesc   string // description of when the operating tick exits
	pcOfBranch uint32 // PC where the operating tick checks this
}

// flags lists every check observed in the operating-tick entry block
// (PC 0x18568..0x185D0). Derived directly from disassembly.
var flags = []flagSpec{
	{"b010 (sweep status snap)", 0xFFB010, bus.Word, 11, 0, "bit 11 set ⇒ exit to 0x18adc", 0x18572},
	{"b1e0 (mode bits)", 0xFFB1E0, bus.Word, -1, 0,
		"(value & 6) != 0 ⇒ branch to 0x191e0 (skip deep path)", 0x18588},
	{"b1e4 (sub-mode)", 0xFFB1E4, bus.Word, -1, 0x34,
		"== 0x34 ⇒ branch to 0x185ac (skip jsr 0x6fa)", 0x18592},
	{"bc64 (display sub-mode)", 0xFFBC64, bus.Word, 13, 0,
		"bit 13 clear AND b1e4 != 0 ⇒ skip jsr 0x6fa branch", 0x1859A},
	{"b07a (display flags)", 0xFFB07A, bus.Word, 11, 0,
		"bit 11 set ⇒ exit to 0x18abc", 0x185AC},
	{"b07c (more display flags)", 0xFFB07C, bus.Word, 13, 0,
		"bit 13 set ⇒ exit to 0x18abc", 0x185B6},
	{"b0ce (state/sweep)", 0xFFB0CE, bus.Word, 11, 0,
		"bit 11 clear ⇒ branch to 0x18642 (skip jsr 0x5af4)", 0x185C4},
}

func main() {
	bootCycles := 30_000_000
	if len(os.Args) > 1 {
		if n, err := strconv.Atoi(os.Args[1]); err == nil && n > 0 {
			bootCycles = n
		}
	}

	rom, err := romloader.LoadDir("hp8593a_eeproms")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	m, err := machine.New8593A(rom)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	m.CPU.Reset()
	m.BootToOperating(bootCycles)

	fmt.Println("Operating-tick early-exit flags (post-boot):")
	fmt.Println()
	for _, f := range flags {
		v := m.Bus.Read(f.addr, f.width)
		switch {
		case f.bit >= 0:
			bit := (v >> uint(f.bit)) & 1
			fmt.Printf("  PC=%06X  %-30s addr=%08X  value=%04X  bit%d=%d  → %s\n",
				f.pcOfBranch, f.name, f.addr, v, f.bit, bit, f.exitDesc)
		default:
			match := "≠"
			if v == f.cmp {
				match = "=="
			}
			fmt.Printf("  PC=%06X  %-30s addr=%08X  value=%04X  %s%04X  → %s\n",
				f.pcOfBranch, f.name, f.addr, v, match, f.cmp, f.exitDesc)
		}
	}

	// Set the key flag (IRQ3 would normally do this) so we can verify
	// the bclr at PC 0x18F42 fires when the tick runs deep enough.
	m.CPU.SetIRQ(3)
	m.CPU.Run(400)
	m.CPU.SetIRQ(0)
	bc67Before := uint32(m.Bus.Read(0xFFBC67, bus.Byte))
	fmt.Printf("\nIRQ3 injected ⇒ bc67 = %02X (bit 0 = key-available flag)\n", bc67Before)

	// Now experiment: pre-arm flags so the operating tick takes the
	// EARLY EXIT at PC 0x18578 to 0x18ADC, which is the path-prefix
	// that leads to the key bclr at 0x18F42. (The disasm shows xrefs
	// FROM 0x18578 + 0x18584 INTO 0x18ADC; from there the function
	// flows through 0x18B00 → 0x18E20 → 0x18E54 → 0x18E62 → 0x18E6E
	// → 0x18E76 → 0x18E7A → bra 0x18F42.)
	fmt.Println("\nPre-arming flags to take the EARLY-EXIT-to-0x18ADC path:")
	fmt.Println("  b010 bit 11 := 1  (so bne at 0x18578 branches to 0x18ADC)")
	fmt.Println("  b1e0 := 0x0200    (bit 9 set so 0x18AFC doesn't skip to 0x18FD6)")
	fmt.Println("  befa bit 10 := 0  (so 0x18B00 doesn't skip to 0x18FD6)")
	fmt.Println("  9afb bit 2  := 1  (so 0x18E6E falls through to jsr+bra-to-bclr)")
	b010 := uint32(m.Bus.Read(0xFFB010, bus.Word)) | (1 << 11)
	m.Bus.Write(0xFFB010, bus.Word, b010)
	m.Bus.Write(0xFFB1E0, bus.Word, 0x0200)
	befaInit := uint32(m.Bus.Read(0xFFBEFA, bus.Word)) & ^uint32(1<<10)
	m.Bus.Write(0xFFBEFA, bus.Word, befaInit)
	b9afb := uint32(m.Bus.Read(0xFF9AFB, bus.Byte)) | (1 << 2)
	m.Bus.Write(0xFF9AFB, bus.Byte, b9afb)
	// LAYER 2 investigation: b0ab=0x12 would normally be set by the
	// firmware at PC 0x1B310 IF the dispatch at slot 0x5C8 returned
	// D0 with bit 0 set. With b0ab non-zero, fcn.A250 (which sets
	// b0ce.11) runs to completion instead of early-exiting. Pre-arm
	// it to see whether the operating tick takes a different path.
	m.Bus.Write(0xFFB0AB, bus.Byte, 0x12)
	fmt.Println("  b0ab := 0x12  (LAYER 2: enables fcn.A250 which sets b0ce.11 dynamically)")

	fmt.Println("\nForcing operating tick (10M cycles, instrumented):")

	// Instrument: step one instruction at a time, recording every PC
	// visited and every PC that lands BACK in fcn.18568's body (so we
	// can see how deep into the operating tick the function progresses).
	visited := make(map[uint32]int)
	const tickRangeLo = 0x18568
	const tickRangeHi = 0x19200
	maxTickPC := uint32(0)
	visitsInsideTick := 0

	// Direct-force experiment: jump straight to PC 0x18F42 (the bclr
	// for bc67 bit 0). If this clears the key flag, the chain itself
	// works and we know the question is purely about reaching that PC.
	fmt.Printf("\n>>> Direct force PC = 0x18F42 (the bclr) — 1K cycles <<<\n")
	m.CPU.SetReg(cpu.PC, 0x18F42)
	m.CPU.Run(1000)
	bc67Direct := uint32(m.Bus.Read(0xFFBC67, bus.Byte))
	fmt.Printf("  After: bc67=%02X (bit 0 = %d)  PC=%06X\n",
		bc67Direct, bc67Direct&1, m.CPU.Reg(cpu.PC))
	if bc67Direct&1 == 0 {
		fmt.Println("  ✓ direct-force bclr works — the question is purely path-reach")
	}

	// Re-prime bc67 so the proper instrumented test below starts fresh.
	m.CPU.SetIRQ(3)
	m.CPU.Run(400)
	m.CPU.SetIRQ(0)
	fmt.Printf("  IRQ3 re-injected: bc67=%02X\n",
		uint32(m.Bus.Read(0xFFBC67, bus.Byte)))

	// Force PC directly to 0x18ADC — the deep-block entry. With the
	// pre-arms above (including the b0ab=0x12 LAYER 2 probe that
	// enables fcn.A250 to set b0ce.11 dynamically when reached), the
	// deep path fires the key bclr at PC 0x18F42.
	fmt.Println("\n>>> Force PC = 0x18ADC (deep-block entry) <<<")
	m.CPU.SetReg(cpu.PC, 0x18ADC)
	const maxInstrumented = 20_000_000
	for step := 0; step < maxInstrumented; step++ {
		pc := m.CPU.Reg(cpu.PC)
		visited[pc]++
		if pc >= tickRangeLo && pc <= tickRangeHi {
			visitsInsideTick++
			if pc > maxTickPC {
				maxTickPC = pc
			}
		}
		m.CPU.Run(1)
		// Periodic IRQ5 inject (every ~500 steps) so timer waits advance.
		if step%500 == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Run(400)
			m.CPU.SetIRQ(0)
		}
	}
	exitPC := m.CPU.Reg(cpu.PC)
	fmt.Printf("  ran %d steps; final PC=%06X\n", maxInstrumented, exitPC)
	fmt.Printf("  visited %d distinct PCs total; %d visits inside fcn.18568 body\n",
		len(visited), visitsInsideTick)
	fmt.Printf("  deepest PC inside fcn.18568: %06X\n", maxTickPC)

	// Did we reach the key bclr at 0x18F42 or sweep-done bclr region at 0x17346?
	for _, target := range []uint32{0x18F42, 0x17346, 0x191E0, 0x18ABC, 0x18ADC, 0x19088, 0x19098} {
		if visited[target] > 0 {
			fmt.Printf("  ✓ reached PC=%06X (visited %d times)\n", target, visited[target])
		} else {
			fmt.Printf("  ✗ never reached PC=%06X\n", target)
		}
	}

	// Show a count of visits per 0x100-byte bucket in the tick range —
	// quick view of where execution concentrated.
	type bucketCount struct {
		bucket uint32
		count  int
	}
	buckets := make(map[uint32]int)
	for pc, n := range visited {
		buckets[pc&^0xFF] += n
	}
	bs := make([]bucketCount, 0, len(buckets))
	for b, c := range buckets {
		bs = append(bs, bucketCount{b, c})
	}
	sort.Slice(bs, func(i, j int) bool { return bs[i].count > bs[j].count })
	fmt.Println("  top 8 PC buckets by visit count:")
	for i, b := range bs {
		if i >= 8 {
			break
		}
		fmt.Printf("    0x%06X-0x%06X: %d visits\n", b.bucket, b.bucket+0xFF, b.count)
	}

	// Re-dump the flags to see post-tick state.
	fmt.Println("\nPost-tick flag values:")
	for _, f := range flags {
		v := m.Bus.Read(f.addr, f.width)
		fmt.Printf("  %-30s addr=%08X  value=%04X\n", f.name, f.addr, v)
	}

	// The 0x188B6 loop check — these comparison pairs need to be
	// EQUAL for the firmware to continue past the loop:
	fmt.Println("\n  Loop-exit condition values (need to be EQUAL to continue):")
	for _, pair := range []struct {
		name     string
		a, b     uint32
	}{
		{"bbba ?= bbbc", 0xFFBBBA, 0xFFBBBC},
		{"a630 ?= a632", 0xFFA630, 0xFFA632},
		{"bc26 ?= bc28", 0xFFBC26, 0xFFBC28},
	} {
		va := m.Bus.Read(pair.a, bus.Word)
		vb := m.Bus.Read(pair.b, bus.Word)
		eq := "=="
		if va != vb {
			eq = "≠"
		}
		fmt.Printf("    %-15s  %04X %s %04X\n", pair.name, va, eq, vb)
	}
	b1f8 := m.Bus.Read(0xFFB1F8, bus.Word)
	fmt.Printf("    b1f8 & 0x1800 == 0x1800? %04X & 0x1800 = %04X (need == 0x1800)\n",
		b1f8, b1f8&0x1800)

	// And the sweep-done flag specifically.
	befa := m.Bus.Read(0xFFBEFA, bus.Word)
	bc67After := uint32(m.Bus.Read(0xFFBC67, bus.Byte))
	fmt.Printf("\n  befa = %04X (bit 13 / sweep-done = %d)\n",
		befa, (befa>>13)&1)
	fmt.Printf("  bc67 = %02X (bit 0 / key-flag = %d) — was %02X before tick\n",
		bc67After, bc67After&1, bc67Before)
	if bc67Before&1 != 0 && bc67After&1 == 0 {
		fmt.Println("  ✓ bclr at PC 0x18F42 FIRED — key flag cleared end-to-end!")
	} else if bc67Before&1 != 0 {
		fmt.Println("  ✗ key flag still set — tick didn't reach the bclr")
	}
}
