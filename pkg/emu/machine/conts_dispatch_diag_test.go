package machine_test

import (
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
)

// TestCONTSDispatchDiag — the Gate-1 unlock experiment (2026-07-12).
//
// The softkey-dispatch RE established that typed CONTS is a hard NO-OP by
// design (class 0x10 → zero jump-table entry in fcn.12288), and the CONTS
// action exists only as command CLASS 0x27: fcn.12288 case @0x126d4 →
// slot 0x550 → fcn.5f968, whose arg (incoming d0 high word) bit0 = desired
// on/off state; on mismatch it does `bchg #3, b0a1` (CONTS) and runs the
// continuous-sweep arm code. The minimal legitimate dispatch — exactly what
// the SWEEP→CONT softkey emitter would produce — is:
//
//	push.w #0x2701 ; d0 = 0x00010000 ; d1 = 0 ; jsr fcn.12288
//
// This test boots to the operating loop, performs that call via the
// firmware's own dispatcher (no RAM-cell forcing), and measures what
// unlocks: b0a1.3 (CONTS), the sweep-arm counter a9a0 (boot value -1 =
// disabled), and the display vector count (the trace-paint proxy).
func TestCONTSDispatchDiag(t *testing.T) {
	if testing.Short() {
		t.Skip("boot-length diagnostic")
	}
	m := newMachine(t)
	m.MMIO.SweepActive = true
	m.BootToOperatingWithSweep(150_000_000)

	rdB := func(a uint32) uint32 { return m.Bus.Read(a, bus.Byte) }
	rdW := func(a uint32) uint32 { return m.Bus.Read(a, bus.Word) }
	t.Logf("pre : b0a1=%#02x (CONTS bit3=%d)  a9a0=%#04x  paints=%d",
		rdB(0xFFB0A1), (rdB(0xFFB0A1)>>3)&1, rdW(0xFFA9A0), m.MMIO.Display.Paints)

	// The CONTS handler's restart-path preconditions (fcn.5f968 bails):
	//   b068.l|b06c.l must be 0; b0a2.l (sweep time µs) ≥ 0x4e20 (20 ms);
	//   then fcn.5d4→0x9568 (set sweep time) → 0x9636 bsr fcn.8f04 (arm).
	rdL := func(a uint32) uint32 { return m.Bus.Read(a, bus.Long) }
	t.Logf("pre-cond: b068=%#x b06c=%#x b0a2=%d(µs) b1e4=%#x",
		rdL(0xFFB068), rdL(0xFFB06C), rdL(0xFFB0A2), rdW(0xFFB1E4))

	// Build the minimal fcn.12288 call frame on the live stack:
	//   sp-2: arg word 0x2701 (class 0x27, low byte arbitrary)
	//   sp-6: return address = sentinel (RunUntil stops on PC fetch)
	const sentinel = 0x000FFFFC // even, in ROM, never naturally executed
	sp := m.CPU.Reg(cpu.A7)
	sp -= 2
	m.Bus.Write(sp, bus.Word, 0x2701)
	sp -= 4
	m.Bus.Write(sp, bus.Long, sentinel)
	m.CPU.SetReg(cpu.A7, sp)
	m.CPU.SetReg(cpu.D0, 0x00010000) // high word bit0 = 1 → CONTS ON
	m.CPU.SetReg(cpu.D1, 0)
	m.CPU.SetReg(cpu.PC, 0x00012288)

	if _, hit := m.CPU.RunUntil(2_000_000, sentinel); !hit {
		t.Fatalf("fcn.12288 did not return to sentinel (PC=%#06x)", m.CPU.Reg(cpu.PC))
	}
	// Known (checkpoint-traced): with b068.l != 0 the CONTS handler bails right
	// after the bchg — the sweep-time recompute (fcn.5d4→0x9568→0x9636→fcn.8f04)
	// is skipped, so CONTS toggles but nothing arms. The natural arm lever is
	// the set-sweep-time entry itself (slot fcn.5d4 = jmp 0x9568), which every
	// freq/span/sweep-time change funnels through. Invoke it with the current
	// sweep time (b0a2) and checkpoint the arm.
	callFn := func(entry uint32, d0 uint32) {
		sp := m.CPU.Reg(cpu.A7) - 4
		m.Bus.Write(sp, bus.Long, sentinel)
		m.CPU.SetReg(cpu.A7, sp)
		m.CPU.SetReg(cpu.D0, d0)
		m.CPU.SetReg(cpu.PC, entry)
	}
	// runToWithTicks runs toward a target PC while pumping IRQ5 timer ticks —
	// the firmware's helpers busy-wait on the tick counter 0xbf12 (see the A7
	// map note), so a bare RunUntil stalls in the delay loops.
	runToWithTicks := func(target uint32, chunks int) bool {
		for i := 0; i < chunks; i++ {
			if _, hit := m.CPU.RunUntil(20_000, target); hit {
				return true
			}
			m.CPU.SetIRQ(5)
			m.CPU.Run(400)
			m.CPU.SetIRQ(0)
		}
		return false
	}
	callFn(0x9568, rdL(0xFFB0A2)) // set sweep time = current value
	for _, cp := range []struct {
		pc   uint32
		what string
	}{
		{0x9636, "0x9568 → fcn.8f04 arm call"},
		{0x90c8, "fcn.8f04 ARM path"},
		{sentinel, "0x9568 returned"},
	} {
		if !runToWithTicks(cp.pc, 400) {
			t.Logf("checkpoint NOT reached: %#06x %s (PC=%#06x)", cp.pc, cp.what, m.CPU.Reg(cpu.PC))
			break
		}
		t.Logf("checkpoint hit: %#06x %s", cp.pc, cp.what)
	}
	t.Logf("after set-sweep-time: a9a0=%#04x  bf34=%#x", rdW(0xFFA9A0), rdL(0xFFBF34))

	conts := (rdB(0xFFB0A1) >> 3) & 1
	t.Logf("post: b0a1=%#02x (CONTS bit3=%d)  a9a0=%#04x", rdB(0xFFB0A1), conts, rdW(0xFFA9A0))
	if conts != 1 {
		t.Fatalf("CONTS (b0a1.3) not set by legitimate class-0x27 dispatch")
	}

	// Resume the operating loop with the sweep drive and see what the
	// continuous-sweep state unlocks (sweep re-arm a9a0, display activity).
	paints0 := m.MMIO.Display.Paints
	m.CPU.SetReg(cpu.PC, 0x00018568) // re-enter the operating tick loop
	m.BootToOperatingWithSweep(120_000_000)
	t.Logf("after resume: a9a0=%#04x  b0a1.3=%d  paints delta=%d",
		rdW(0xFFA9A0), (rdB(0xFFB0A1)>>3)&1, m.MMIO.Display.Paints-paints0)
}
