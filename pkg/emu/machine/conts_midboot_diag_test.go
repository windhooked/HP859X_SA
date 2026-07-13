package machine_test

import (
	"image/png"
	"os"
	"testing"

	"github.com/windhooked/HP859X_SA/internal/emutest"
	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
)

// TestCONTSMidBootDiag — sweep-restart experiment v3 (2026-07-13).
//
// v1 proved the class-0x27 dispatch sets CONTS; v2 showed the boot's own
// sweep-arm (fcn.8f04) calls all happen before 60M cycles, so a post-hoc or
// mid-boot dispatch misses them. v3 intercepts the FIRST fcn.8f04 call during
// a sweep-driven boot: save all registers, run the legitimate CONTS dispatch
// (fcn.12288 class 0x27), restore registers, and let fcn.8f04 proceed with
// CONTS now set — its 0x8f5a `btst #3,b0a1` should then take the 0x90c8 ARM
// path (a9a0 armed = continuous sweep) instead of the 0x92b2 abort (a9a0=-1).
func TestCONTSMidBootDiag(t *testing.T) {
	if testing.Short() {
		t.Skip("boot-length diagnostic")
	}
	m := newMachine(t)
	m.MMIO.SweepActive = true
	m.SweepDrive = true

	rdB := func(a uint32) uint32 { return m.Bus.Read(a, bus.Byte) }
	rdW := func(a uint32) uint32 { return m.Bus.Read(a, bus.Word) }
	rdL := func(a uint32) uint32 { return m.Bus.Read(a, bus.Long) }

	// Custom boot loop (mirrors bootLoop: 2000-cycle chunks, LoopBreaker,
	// IRQ5 pump every 5th chunk, sweep drive) that stops at fcn.8f04 entry.
	const (
		chunk      = 2000
		armEntry   = 0x00008f04
		armPath    = 0x000090c8
		abortPath  = 0x000092b2
		dispatcher = 0x00012288
		sentinel   = 0x000FFFFC
	)
	lb := emutest.NewLoopBreaker(50)
	hit := false
	for i := 0; i < 100_000 && !hit; i++ { // ≤200M cycles
		if _, h := m.CPU.RunUntil(chunk, armEntry); h {
			hit = true
			break
		}
		lb.Check(m.CPU.Reg(cpu.PC), m.CPU.SetReg)
		if i%5 == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Run(400)
			m.CPU.SetIRQ(0)
		}
		m.DriveOneSweepChunk()
	}
	if !hit {
		t.Fatalf("fcn.8f04 never reached during boot (PC=%#06x)", m.CPU.Reg(cpu.PC))
	}
	t.Logf("first fcn.8f04 hit: a9a0=%#04x b0a1.3=%d", rdW(0xFFA9A0), (rdB(0xFFB0A1)>>3)&1)

	// Save full register state at fcn.8f04 entry.
	var saved [18]uint32
	for _, r := range cpu.All {
		saved[int(r)] = m.CPU.Reg(r)
	}

	// Legitimate CONTS ON dispatch via the firmware's class dispatcher.
	sp := m.CPU.Reg(cpu.A7) - 64 // scratch below live stack
	m.Bus.Write(sp-2, bus.Word, 0x2701)
	m.Bus.Write(sp-6, bus.Long, sentinel)
	m.CPU.SetReg(cpu.A7, sp-6)
	m.CPU.SetReg(cpu.D0, 0x00010000)
	m.CPU.SetReg(cpu.D1, 0)
	m.CPU.SetReg(cpu.PC, dispatcher)
	if _, h := m.CPU.RunUntil(2_000_000, sentinel); !h {
		t.Fatalf("dispatch did not return (PC=%#06x)", m.CPU.Reg(cpu.PC))
	}
	// Restore the intercepted fcn.8f04 context exactly.
	for _, r := range cpu.All {
		m.CPU.SetReg(r, saved[int(r)])
	}
	t.Logf("dispatched at fcn.8f04 entry: b0a1.3=%d", (rdB(0xFFB0A1)>>3)&1)

	// Watchpoint: log non-{0,0x10} writes to the b068/b06c gate pair with PC.
	wpLogs := 0
	m.Bus.OnWrite = func(addr uint32, sz bus.Size, val uint32) {
		if addr >= 0xFFB068 && addr < 0xFFB070 && val != 0 && val != 0x10 && wpLogs < 16 {
			wpLogs++
			t.Logf("  b068-block write: [%#x] <- %#x (sz %d) PC=%#06x", addr, val, sz, m.CPU.Reg(cpu.PC))
		}
	}
	defer func() { m.Bus.OnWrite = nil }()

	// Continue the boot, logging EVERY subsequent fcn.8f04 entry's gate state
	// (b068/b06c must be 0 for the arm; the tune path clears them) and every
	// 0x90c8 ARM hit — the full natural picture of when the arm could fire.
	paints0 := m.MMIO.Display.Paints
	entries, arms := 0, 0
	for i := 0; i < 100_000; i++ { // ~200M cycles
		if _, h := m.CPU.RunUntil(chunk, armEntry); h {
			entries++
			if entries <= 12 {
				t.Logf("fcn.8f04 entry #%d: b068=%#x b06c=%#x b0a2=%d a9a0=%#04x",
					entries, rdL(0xFFB068), rdL(0xFFB06C), rdL(0xFFB0A2), rdW(0xFFA9A0))
			}
			// step past the entry so RunUntil can re-trigger on the next call
			if _, h2 := m.CPU.RunUntil(200_000, armPath); h2 {
				arms++
				if arms <= 4 {
					t.Logf("  ★ → ARM path 0x90c8 (a9a0 about to load)")
				}
			}
			continue
		}
		lb.Check(m.CPU.Reg(cpu.PC), m.CPU.SetReg)
		if i%5 == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Run(400)
			m.CPU.SetIRQ(0)
		}
		m.DriveOneSweepChunk()
	}
	t.Logf("fcn.8f04 entries=%d, ARM-path hits=%d", entries, arms)
	t.Logf("final: b0a1.3=%d a9a0=%#04x bf34=%#x paints delta=%d",
		(rdB(0xFFB0A1)>>3)&1, rdW(0xFFA9A0), rdL(0xFFBF34), m.MMIO.Display.Paints-paints0)

	if a9a0 := rdW(0xFFA9A0); a9a0 != 0xFFFF {
		t.Logf("★★ a9a0 ARMED = %#04x — continuous sweep unlocked", a9a0)
	}

	// Render the screen for visual inspection (project rule: PNGs → screens/).
	if f, err := os.Create("../../../screens/conts_midboot.png"); err == nil {
		png.Encode(f, m.MMIO.Display.RenderFrame())
		f.Close()
	}
}
