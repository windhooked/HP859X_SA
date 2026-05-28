// Package emutest provides helpers for the emulator test framework:
//
//   - DiffCores: runs an instruction sequence on two cpu.CPU implementations
//     side-by-side and reports any register divergence.
//   - RunUntilPC: drives a cpu.CPU until it reaches a target PC or exceeds a
//     step budget (boot-milestone assertion helper).
package emutest

import (
	"fmt"
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
)

// DiffCores steps both a and b by n instructions starting from their current
// state and reports any register difference as a test failure. Both cores must
// already be loaded with the same image and reset to the same starting state
// before calling DiffCores.
//
// On divergence the test continues (t.Errorf, not t.Fatalf) so all differing
// registers are reported in a single run.
func DiffCores(t *testing.T, a, b cpu.CPU, aName, bName string, steps int) {
	t.Helper()
	for step := 1; step <= steps; step++ {
		if err := a.Step(); err != nil {
			t.Fatalf("DiffCores step %d %s.Step: %v", step, aName, err)
		}
		if err := b.Step(); err != nil {
			t.Fatalf("DiffCores step %d %s.Step: %v", step, bName, err)
		}
		for _, r := range cpu.All {
			av, bv := a.Reg(r), b.Reg(r)
			if av != bv {
				t.Errorf("step %d reg %-2s: %s=%08X  %s=%08X",
					step, r, aName, av, bName, bv)
			}
		}
		if t.Failed() {
			// Stop at first diverging step — no value in continuing
			// since subsequent state is built on the mismatch.
			t.Logf("DiffCores aborted at step %d due to register divergence", step)
			return
		}
	}
}

// RunUntilPC steps c until PC equals target, returning the step count.
// Returns an error if maxSteps is exceeded before target is reached.
func RunUntilPC(c cpu.CPU, target uint32, maxSteps int) (int, error) {
	for i := 1; i <= maxSteps; i++ {
		if err := c.Step(); err != nil {
			return i, fmt.Errorf("step %d: %w", i, err)
		}
		if c.Reg(cpu.PC) == target {
			return i, nil
		}
	}
	return maxSteps, fmt.Errorf("PC never reached %#08X after %d steps (last PC=%#08X)",
		target, maxSteps, c.Reg(cpu.PC))
}

// LoopBreaker breaks known firmware busy-wait / delay loops so that boot
// tests finish in a reasonable time budget without corrupting functional
// loops (e.g. RAM-test march patterns). Call it after each Run() chunk.
//
// HP 8593A Rev L 98.06.15 Opt-027 known loops — see hp8593aLoops below.
//
// All other PC ranges are left untouched.
type LoopBreaker struct {
	counts    map[uint32]int // key: loop-lo; count of in-range chunks
	outside   int            // consecutive chunks with PC outside all loops
	thresh    int            // in-range chunks before breaking
	hysteresis int           // outside chunks before resetting in-range counts
}

// NewLoopBreaker returns a LoopBreaker that triggers after thresh consecutive
// Run() calls with PC in the same loop range. hysteresis is the number of
// consecutive out-of-range calls required before the loop counts are reset
// (prevents brief excursions — e.g. the NOT sub-loop at 0x2184 between two
// compare-body iterations — from resetting the count prematurely).
func NewLoopBreaker(thresh int) *LoopBreaker {
	return &LoopBreaker{
		counts:     make(map[uint32]int),
		thresh:     thresh,
		hysteresis: 10, // 10 consecutive out-of-range chunks to reset counts
	}
}

// loopDef describes a firmware delay / test loop that LoopBreaker can
// force-exit.
type loopDef struct {
	lo, hi uint32  // inclusive PC range of the loop body
	reg    cpu.Reg // register to overwrite on break
	val    uint32  // break value (0 for countdown regs; end-address−2 for A2)
}

// hp8593aLoops lists the delay / test loops observed during HP 8593A Rev L
// 98.06.15 Opt-027 boot.
//
//   - 0x454A–0x456A: ROM checksum inner loop. Eight 16-bit unrolled
//     `add.b (A0)+, D2/D3` summing pairs, then `dbra D0, $454A`. The outer
//     loop at 0x458A `dbra D5, $453E` iterates segments; setting D0=0 here
//     also implicitly bounds total work because the firmware re-enters the
//     inner loop with a fresh D0 each segment. (Rev L equivalent of the
//     17.12.90 `0x1F5A` ROM checksum loop.)
//   - 0x4784–0x47F6: March RAM test. Two sub-loops calling a shared check
//     body via `jmp (A3)`:
//         sub-loop 1: A3=0x4786 `not.w (A2)+; cmpa.l A2,A1; bne $4784`
//         sub-loop 2: A3=0x4796 (same idiom with D0 NOTed)
//     The check body at 0x47D8..0x47F6 reads (A2)/(A2+1), compares to D0,
//     accumulates errors into D1, then jmps back to A3. A1=0xFFC000
//     (end-of-RAM), A0=0xFEC000 (start). Break value A2=0xFFBFFE=A1-2:
//     the next `not.w (A2)+` advances A2 to 0xFFC000=A1, then `cmpa.l A2,A1`
//     sets Z, `bne` falls through, exiting the sub-loop. RAM at 0xFFBFFE..
//     0xFFC000 is zeroed so D7 stays 0 and D1 stays 0 (test passes).
//     (Rev L equivalent of 17.12.90's 0x2182–0x21F6 march RAM test.)
//
// More loops will be added as they are observed in `cmd/tracestall` output.
var hp8593aLoops = [...]loopDef{
	{lo: 0x454A, hi: 0x456A, reg: cpu.D0, val: 0x00000000}, // Rev L ROM checksum inner
	{lo: 0x4784, hi: 0x47F6, reg: cpu.A2, val: 0x00FFBFFE}, // Rev L march RAM test
}

// Check examines pc and, if it is inside a known delay loop that has been
// running for more than thresh consecutive chunks, force-exits it by setting
// the loop-control register to the break value via setter.
// Returns true if a loop was broken this call.
func (lb *LoopBreaker) Check(pc uint32, setter func(r cpu.Reg, v uint32)) bool {
	for i := range hp8593aLoops {
		l := &hp8593aLoops[i]
		if pc >= l.lo && pc <= l.hi {
			lb.outside = 0 // back inside a loop; don't count out-of-range
			lb.counts[l.lo]++
			if lb.counts[l.lo] > lb.thresh {
				setter(l.reg, l.val)
				lb.counts[l.lo] = 0
				return true
			}
			return false
		}
	}

	// PC is outside all known loops.
	lb.outside++
	if lb.outside >= lb.hysteresis {
		// Sustained excursion — the loop genuinely exited. Reset counts so
		// the breaker can re-trigger the next time the firmware enters a loop.
		for k := range lb.counts {
			lb.counts[k] = 0
		}
		lb.outside = 0
	}
	return false
}
