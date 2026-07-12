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
