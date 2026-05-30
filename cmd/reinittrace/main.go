// Command reinittrace pins down WHY the firmware re-runs the destructive boot
// RAM test (POST) during operation — the blocker that wipes the sweep/trace
// state (see docs/DRIVETICK_BLOCKER.md / the sweep investigation).
//
// Chunk-sampled probes (cmd/sweeprun) can see the firmware sitting in the long
// checksum/march loops but MISS the brief single-instruction entry transition
// (the IRQ7/NMI vector fetch at 0x3A9E, the POST jsr at 0x3AC2). This tool
// boots to the armed-sweep state with the fast chunked loop, then switches to
// INSTRUCTION-LEVEL single stepping with a ring buffer of the last N PCs. The
// moment PC enters the POST / NMI region it dumps the ring buffer (run-length
// compressed) so the exact control-flow path in — and what the firmware was
// executing when it got there — is visible.
//
// Usage:
//
//	DYLD_FALLBACK_LIBRARY_PATH=/usr/local/lib go run ./cmd/reinittrace/ [maxSteps]
package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/windhooked/HP859X_SA/internal/emutest"
	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
	"github.com/windhooked/HP859X_SA/pkg/emu/machine"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

func main() {
	maxSteps := 40_000_000
	if len(os.Args) > 1 {
		if n, err := strconv.Atoi(os.Args[1]); err == nil && n > 0 {
			maxSteps = n
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

	rdBF34 := func() uint32 { return m.Bus.Read(0xFFBF34, bus.Long) }

	// Phase 1: fast chunked boot until the sweep arms (bf34 == 0x40B8).
	const (
		chunkCycles    = 2000
		breakThresh    = 50
		irq5Period     = 5
		irqServiceCost = 400
	)
	lb := emutest.NewLoopBreaker(breakThresh)
	armed := false
	for done := 0; done < 60_000_000 && !armed; done += chunkCycles {
		m.CPU.Run(chunkCycles)
		lb.Check(m.CPU.Reg(cpu.PC), m.CPU.SetReg)
		chunk := done / chunkCycles
		if chunk%irq5Period == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Run(irqServiceCost)
			m.CPU.SetIRQ(0)
		}
		if rdBF34() == 0x40B8 {
			armed = true
			fmt.Printf("sweep armed after ~%d cycles; bf30=%08X bf34=%08X — switching to single-step trace\n",
				done, m.Bus.Read(0xFFBF30, bus.Long), rdBF34())
		}
	}
	if !armed {
		fmt.Println("sweep never armed within 60M cycles; aborting")
		os.Exit(1)
	}

	// Phase 2: single-step with a ring buffer; stop on entering POST/NMI region.
	const ringSize = 512
	ring := make([]uint32, 0, ringSize)
	push := func(pc uint32) {
		if len(ring) < ringSize {
			ring = append(ring, pc)
		} else {
			copy(ring, ring[1:])
			ring[ringSize-1] = pc
		}
	}
	// Trigger PCs: NMI handler, POST entry, checksum entry, RAM-test fill entry.
	// At each DLP dispatch (PC==0x34C90) record (offset, token); the derail is
	// the first dispatch whose token is out of the handler-table range. Logging
	// the trajectory shows how the record index walks from valid records to the
	// 0x71D03 filler.
	type disp struct{ idx, off, tok, head, tail uint32 }
	var traj []disp
	var lastIdx uint32 // D0 (idx) captured at fcn.331cc entry 0x331CC
	isTrigger := func(pc uint32) bool {
		if pc == 0x331CC {
			lastIdx = m.CPU.Reg(cpu.D0)
			return false
		}
		if pc != 0x34C90 {
			return false
		}
		recPtr := m.Bus.Read(m.CPU.Reg(cpu.A6)-0x1E, bus.Long)
		tok := m.Bus.Read(recPtr, bus.Word)
		off := (recPtr - m.Bus.Read(0x0A50, bus.Long)) & 0xFFFF
		traj = append(traj, disp{lastIdx, off, tok, m.Bus.Read(0xFFA630, bus.Word), m.Bus.Read(0xFFA632, bus.Word)})
		return tok >= 0x200 // out-of-range token ⇒ derail dispatch
	}

	// Inject IRQ5 every ~irq5Steps single-steps to keep the timer advancing
	// (≈ the same 1 tick / 10k cycles cadence the chunked loop used).
	const irq5Steps = 1500
	stepsSinceIRQ := 0
	cyclesApprox := 0

	// Watch the foreground-ring head/tail ($a630/$a632) for the repoint that
	// switches the startup DLP from the VRD __A..__Z source (tail 0xD0) to the
	// garbage source (head 0xD/tail 0x4D). Log the PC that performed each change.
	prevHead := m.Bus.Read(0xFFA630, bus.Word)
	prevTail := m.Bus.Read(0xFFA632, bus.Word)
	type change struct {
		pc, oldH, newH, oldT, newT uint32
	}
	var changes []change

	for i := 0; i < maxSteps; i++ {
		pc := m.CPU.Reg(cpu.PC)
		if isTrigger(pc) {
			fmt.Printf("\nTRIGGER: PC=%06X reached after %d single-steps (~%d cycles)\n",
				pc, i, cyclesApprox)
			fmt.Printf("  SR=%04X A7=%06X bff8=%04X f618=%04X\n",
				m.CPU.Reg(cpu.SR), m.CPU.Reg(cpu.A7),
				m.Bus.Read(0xFFBFF8, bus.Word), m.Bus.Read(0xFFF618, bus.Word))
			// DLP foreground-ring state (the char-source ring the startup DLP
			// runs from) + the recPtr fcn.331cc just built. $a50/$a02 are ROM
			// constants; recPtr should land within [base, base+size); an overrun
			// shows as recPtr past the ROM record region into the 0x71D03 filler.
			a50 := m.Bus.Read(0x0A50, bus.Long)
			recPtr := m.CPU.Reg(cpu.A6) // only meaningful at 0x34C94; read the local instead
			if pc == 0x4762 || pc == 0x4536 || pc == 0x4400 || pc == 0x3A9E {
				recPtr = 0 // POST/NMI triggers — recPtr local not in this frame
			} else {
				recPtr = m.Bus.Read(m.CPU.Reg(cpu.A6)-0x1E, bus.Long)
			}
			fmt.Printf("  DLP ring: base($a62c)=%06X size($a62a)=%04X head($a630)=%04X tail($a632)=%04X\n",
				m.Bus.Read(0xFFA62C, bus.Long), m.Bus.Read(0xFFA62A, bus.Word),
				m.Bus.Read(0xFFA630, bus.Word), m.Bus.Read(0xFFA632, bus.Word))
			fmt.Printf("  $a50=%06X recPtr=%06X recPtr-$a50(offset)=%04X word[recPtr]=%04X $a89e=%04X $a896=%04X\n",
				a50, recPtr, (recPtr-a50)&0xFFFF, m.Bus.Read(recPtr, bus.Word),
				m.Bus.Read(0xFFA89E, bus.Word), m.Bus.Read(0xFFA896, bus.Word))
			// DLP source-include stack: depth $a634, entries of 10 bytes at
			// 0xFFA636 + 10*n (size, base, head, tail). The derail pops to a
			// parent entry whose head/tail point at a source that derails.
			depth := m.Bus.Read(0xFFA634, bus.Word)
			fmt.Printf("  source-stack depth($a634)=%d  entries (n: size base head tail):\n", int16(depth))
			for n := uint32(0); n <= depth+1 && n < 8; n++ {
				e := 0xFFA636 + n*10
				fmt.Printf("    n=%d @%06X: size=%04X base=%06X head=%04X tail=%04X\n",
					n, e, m.Bus.Read(e, bus.Word), m.Bus.Read(e+2, bus.Long),
					m.Bus.Read(e+6, bus.Word), m.Bus.Read(e+8, bus.Word))
			}
			// Trajectory of the last ~24 DLP dispatches: offset into the record
			// table, token there, and the source-ring head/tail at that step.
			fmt.Printf("  --- DLP dispatch trajectory (off, token, head, tail) ---\n")
			start := 0
			if len(traj) > 24 {
				start = len(traj) - 24
			}
			for k := start; k < len(traj); k++ {
				t := traj[k]
				mark := ""
				if t.tok >= 0x200 {
					mark = "  ← DERAIL (token out of range)"
				}
				fmt.Printf("    [%d] idx=%08X off=%04X token=%04X head=%04X tail=%04X%s\n",
					k, t.idx, t.off, t.tok, t.head, t.tail, mark)
			}
			fmt.Printf("  --- head/tail ($a630/$a632) writes (last 16; pc = writing instruction) ---\n")
			cstart := 0
			if len(changes) > 16 {
				cstart = len(changes) - 16
			}
			for k := cstart; k < len(changes); k++ {
				c := changes[k]
				fmt.Printf("    pc=%06X  head %04X→%04X  tail %04X→%04X\n",
					c.pc, c.oldH, c.newH, c.oldT, c.newT)
			}
			dumpRing(ring)
			return
		}
		push(pc)
		if err := m.CPU.Step(); err != nil {
			fmt.Printf("step error at PC=%06X: %v\n", pc, err)
			dumpRing(ring)
			return
		}
		if h, t := m.Bus.Read(0xFFA630, bus.Word), m.Bus.Read(0xFFA632, bus.Word); h != prevHead || t != prevTail {
			changes = append(changes, change{pc, prevHead, h, prevTail, t})
			prevHead, prevTail = h, t
		}
		cyclesApprox += 8
		stepsSinceIRQ++
		if stepsSinceIRQ >= irq5Steps {
			m.CPU.SetIRQ(5)
			m.CPU.Step()
			m.CPU.SetIRQ(0)
			stepsSinceIRQ = 0
		}
	}
	fmt.Printf("no trigger within %d single-steps; final PC=%06X\n", maxSteps, m.CPU.Reg(cpu.PC))
	dumpRing(ring)
}

// dumpRing prints the ring buffer run-length compressed: a tight loop of one PC
// shows as "PC ×N" instead of N lines, so the actual control-flow path stands out.
func dumpRing(ring []uint32) {
	fmt.Printf("  --- last %d PCs leading in (run-length compressed) ---\n", len(ring))
	i := 0
	for i < len(ring) {
		j := i + 1
		for j < len(ring) && ring[j] == ring[i] {
			j++
		}
		n := j - i
		if n > 1 {
			fmt.Printf("    %06X ×%d\n", ring[i], n)
		} else {
			fmt.Printf("    %06X\n", ring[i])
		}
		i = j
	}
}
