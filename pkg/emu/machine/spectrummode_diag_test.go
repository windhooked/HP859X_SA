package machine_test

import (
	"image"
	"image/png"
	"os"
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
)

// TestSpectrumModeDiag — the Gate-1 full-unlock experiment (2026-07-13).
//
// The complete sweep-arm condition in fcn.8f04 (decoded this session):
//
//	CONTS (b0a1.3)  AND  ( b068==0  OR  b0ec==0x31  OR  b0e6>=1 )
//
// b068/b06c is a per-band ROM constant (table 0x20af2) — legitimately nonzero
// in band 0 — so the REAL instrument arms via b0ec == 0x31: SPECTRUM measure
// mode. The mode setter is fcn.21c96(d0=mode) → b0ec, b05a=1 (mode-changed),
// wrapped by fcn.220a0 = jump-slot 0x65e. Boot ends at b0ec=0x01 (CONFIG).
//
// Recipe: after a sweep-driven boot, (1) dispatch CONTS ON through the
// firmware's class dispatcher (fcn.12288, class 0x27 — proven), (2) call the
// mode setter with 0x31, (3) resume the operating loop. Expect: a9a0 arms,
// sweeps run continuously, and the measure-mode DLP paints the trace.
func TestSpectrumModeDiag(t *testing.T) {
	if testing.Short() {
		t.Skip("boot-length diagnostic")
	}
	m := newMachine(t)
	m.MMIO.SweepActive = true
	m.BootToOperatingWithSweep(150_000_000)

	rdB := func(a uint32) uint32 { return m.Bus.Read(a, bus.Byte) }
	rdW := func(a uint32) uint32 { return m.Bus.Read(a, bus.Word) }
	rdL := func(a uint32) uint32 { return m.Bus.Read(a, bus.Long) }
	t.Logf("pre : b0ec=%#02x b0a1.3=%d a9a0=%#04x bf34=%#x paints=%d",
		rdW(0xFFB0EC), (rdB(0xFFB0A1)>>3)&1, rdW(0xFFA9A0), rdL(0xFFBF34), m.MMIO.Display.Paints)

	const sentinel = 0x000FFFFC
	// call runs fn(d0) with an optional pushed word arg, to the sentinel.
	call := func(entry, d0 uint32, arg uint32, hasArg bool) bool {
		sp := m.CPU.Reg(cpu.A7) - 64
		if hasArg {
			sp -= 2
			m.Bus.Write(sp, bus.Word, arg)
		}
		sp -= 4
		m.Bus.Write(sp, bus.Long, sentinel)
		m.CPU.SetReg(cpu.A7, sp)
		m.CPU.SetReg(cpu.D0, d0)
		m.CPU.SetReg(cpu.D1, 0)
		m.CPU.SetReg(cpu.PC, entry)
		// pump timer ticks so firmware delay loops advance
		for i := 0; i < 600; i++ {
			if _, h := m.CPU.RunUntil(20_000, sentinel); h {
				return true
			}
			m.CPU.SetIRQ(5)
			m.CPU.Run(400)
			m.CPU.SetIRQ(0)
		}
		return false
	}

	// (1) CONTS ON via the class dispatcher.
	if !call(0x00012288, 0x00010000, 0x2701, true) {
		t.Fatalf("CONTS dispatch did not return (PC=%#06x)", m.CPU.Reg(cpu.PC))
	}
	t.Logf("CONTS dispatched: b0a1.3=%d", (rdB(0xFFB0A1)>>3)&1)

	// (2) Enter SPECTRUM measure mode: fcn.220a0 (slot 0x65e) with d0=0x31.
	if !call(0x000220a0, 0x31, 0, false) {
		t.Fatalf("mode-set did not return (PC=%#06x)", m.CPU.Reg(cpu.PC))
	}
	t.Logf("mode set: b0ec=%#02x b05a=%#x a9a0=%#04x bf34=%#x",
		rdW(0xFFB0EC), rdW(0xFFB05A), rdW(0xFFA9A0), rdL(0xFFBF34))

	// (3) Resume the operating loop and let the sweep drive run, watching the
	// sweep cycle (A5 sample pointer, befa.13 sweep-done) and the display
	// vector counters (the trace draw = a big Lines/Dots delta).
	// Watchpoint: who writes the trace buffer (first words), with what values?
	wpN, chN := 0, 0
	var lastSel, muxChan uint32
	m.Bus.OnWrite = func(addr uint32, sz bus.Size, val uint32) {
		if addr == 0xFFF75C {
			lastSel = val
		}
		if addr == 0xFFF75E && lastSel == 0x91 { // mux channel select data
			muxChan = val
			if chN < 8 {
				chN++
				t.Logf("  mux channel <- %#x PC=%#06x", val, m.CPU.Reg(cpu.PC))
			}
		}
		if addr >= 0x2FD508 && addr < 0x2FD520 && val == 0xC00 && wpN < 2 {
			wpN++
			t.Logf("  tracebuf 0xC00 write: [%#x] PC=%#06x  lastSel=%#x muxChan=%#x", addr, m.CPU.Reg(cpu.PC), lastSel, muxChan)
		}
	}
	defer func() { m.Bus.OnWrite = nil }()

	d := m.MMIO.Display
	lines0, dots0, glyphs0 := d.Lines, d.Dots, d.Glyphs
	m.CPU.SetReg(cpu.PC, 0x00018568)
	for phase := 1; phase <= 4; phase++ {
		if phase == 4 {
			d.Chip.EnableLineLog() // capture the last phase's line endpoints
		}
		m.BootToOperatingWithSweep(50_000_000)
		t.Logf("resume +%dM: a9a0=%#04x bf34=%#x A5=%#x befa=%#04x lines+%d dots+%d glyphs+%d",
			phase*50, rdW(0xFFA9A0), rdL(0xFFBF34), m.CPU.Reg(cpu.A5), rdW(0xFFBEFA),
			d.Lines-lines0, d.Dots-dots0, d.Glyphs-glyphs0)
	}
	// Where do the lines land? Histogram the captured endpoints.
	if log := d.Chip.LineLog; len(log) > 0 {
		minX, maxX, minY, maxY := 1<<30, -(1 << 30), 1<<30, -(1 << 30)
		for _, r := range log {
			for _, v := range [2]int{r.X0, r.X1} {
				if v < minX {
					minX = v
				}
				if v > maxX {
					maxX = v
				}
			}
			for _, v := range [2]int{r.Y0, r.Y1} {
				if v < minY {
					minY = v
				}
				if v > maxY {
					maxY = v
				}
			}
		}
		t.Logf("line log: %d lines, X∈[%d,%d] Y∈[%d,%d]", len(log), minX, maxX, minY, maxY)
		for i := 0; i < len(log) && i < 8; i++ {
			t.Logf("  line[%d]: (%d,%d)→(%d,%d)", i, log[i].X0, log[i].Y0, log[i].X1, log[i].Y1)
		}
		// a mid-log sample (the boot-graticule repaint dominates the head)
		for i := len(log) / 2; i < len(log) && i < len(log)/2+8; i++ {
			t.Logf("  line[%d]: (%d,%d)→(%d,%d)", i, log[i].X0, log[i].Y0, log[i].X1, log[i].Y1)
		}
	}
	t.Logf("final: b0ec=%#02x b0a1.3=%d a9a0=%#04x", rdW(0xFFB0EC), (rdB(0xFFB0A1)>>3)&1, rdW(0xFFA9A0))

	// Raw capture buffer (0x2FD508, 401 words): nonzero samples = the ADC path
	// feeds real data; all-zero = the capture (peak-detect handler 0x410a) path
	// stores nothing in this mode.
	nz, max := 0, uint32(0)
	for i := 0; i < 401; i++ {
		v := rdW(0x2FD508 + uint32(i*2))
		if v != 0 {
			nz++
		}
		if v > max {
			max = v
		}
	}
	t.Logf("capture buffer: nonzero=%d/401 max=%#x  first8=[%x %x %x %x %x %x %x %x]",
		nz, max,
		rdW(0x2FD508), rdW(0x2FD50A), rdW(0x2FD50C), rdW(0x2FD50E),
		rdW(0x2FD510), rdW(0x2FD512), rdW(0x2FD514), rdW(0x2FD516))

	// Video-filter state: the IRQ6 scaling block (0x4092-0x40b0) is an EMA
	// whose coefficient ada8 comes from fcn.2150e(d0=b078&0xF, arg=b1ea) called
	// from fcn.8f04's tail. ada6!=0 with ada8==0 → every sample = bf2e>>3 =
	// constant (the flat 0xC00 trace).
	t.Logf("video filter: ada6=%#x ada8=%#x bf2e=%#x  b078=%#x (idx=%d) b1ea=%#x bf3c=%#x",
		rdW(0xFFADA6), rdW(0xFFADA8), rdW(0xFFBF2E), rdW(0xFFB078), rdW(0xFFB078)&0xF,
		rdW(0xFFB1EA), rdW(0xFFBF3C))
	t.Logf("f200 bus reads: %#x %#x %#x  (SweepActive=%v)",
		m.Bus.Read(0xFFF200, bus.Word), m.Bus.Read(0xFFF200, bus.Word),
		m.Bus.Read(0xFFF200, bus.Word), m.MMIO.SweepActive)

	// SweepEngine window state: did the mode-set retune collapse the window?
	se := m.MMIO.Sweep
	start, stop := se.Window()
	fmDAC, fineDAC, coarseDAC := m.MMIO.YTOCoilDACs()
	t.Logf("sweep window: TuneActive=%v [%.1f, %.1f] MHz  coils fm=%#x fine=%#x coarse=%#x  ADC samples: %d %d %d",
		se.TuneActive, start/1e6, stop/1e6, fmDAC, fineDAC, coarseDAC,
		se.DetectADC(), se.DetectADC(), se.DetectADC())

	// Locked assertions — the Gate-1 unlock invariants.
	if got := rdW(0xFFB0EC); got != 0x31 {
		t.Errorf("b0ec = %#x, want 0x31 (spectrum mode)", got)
	}
	if (rdB(0xFFB0A1)>>3)&1 != 1 {
		t.Error("CONTS (b0a1.3) not set")
	}
	if a9a0 := rdW(0xFFA9A0); a9a0 == 0xFFFF || a9a0 == 0 {
		t.Errorf("a9a0 = %#04x — sweep not armed", a9a0)
	} else {
		t.Logf("★★ a9a0 ARMED = %#04x", a9a0)
	}
	if nz < 350 {
		t.Errorf("trace buffer nonzero = %d/401 — polled video path not feeding", nz)
	}

	// PERSISTENCE-COMPOSITE render: the trace draws incrementally (sweeping-pen
	// style, erased ahead of the pen), so any single frame holds only fragments
	// — exactly what a CRT integrates with phosphor persistence. Composite the
	// lit pixels of live frames across ~3 sweeps: the union shows the full
	// trace the firmware painted, over the last frame as the base.
	m.MMIO.Display.Chip.SetRenderLive(true)
	var composite *image.RGBA
	litEver := map[int]struct{}{}
	for i := 0; i < 3000; i++ { // ~6M cycles ≈ 3+ sweeps
		m.CPU.Run(2000)
		if i%5 == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Run(400)
			m.CPU.SetIRQ(0)
		}
		m.DriveOneSweepChunk()
		if i%20 != 0 {
			continue
		}
		fr := m.MMIO.Display.RenderFrame()
		if composite == nil {
			composite = image.NewRGBA(fr.Bounds())
		}
		b := fr.Bounds()
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				r, g, _, _ := fr.At(x, y).RGBA()
				if r>>8 > 0x40 || g>>8 > 0x40 {
					composite.Set(x, y, fr.At(x, y))
					litEver[y*1024+x] = struct{}{}
				}
			}
		}
	}
	// Trace evidence: lit-ever pixels in the graph interior vs a single frame.
	single := m.MMIO.Display.RenderFrame()
	litSingle := 0
	for k := range litEver {
		x, y := k%1024, k/1024
		if x >= 8 && x <= 400 && y >= 16 && y <= 200 {
			r, g, _, _ := single.At(x, y).RGBA()
			if r>>8 > 0x40 || g>>8 > 0x40 {
				litSingle++
			}
		}
	}
	litComposite := 0
	for k := range litEver {
		x, y := k%1024, k/1024
		if x >= 8 && x <= 400 && y >= 16 && y <= 200 {
			litComposite++
		}
	}
	t.Logf("graph-interior lit pixels: composite=%d single-frame=%d (delta=%d = persistence-only trace pixels)",
		litComposite, litSingle, litComposite-litSingle)
	// KNOWN GAP (2026-07-14): the trace polyline draws at Y=0 regardless of the
	// capture values — the draw reads a DISPLAY trace array that stays zeroed;
	// the capture→display processing/copy step (fcn.171f6 chain) is the next
	// link (see MEASURE_MODE_HANDOFF.md). When it runs, the composite delta
	// should jump to ≥400 (one pixel per trace column) — promote this log to an
	// assertion then.
	if litComposite-litSingle >= 400 {
		t.Logf("★★★ visible trace painted (delta=%d)", litComposite-litSingle)
	}
	if f, err := os.Create("../../../screens/spectrum_mode.png"); err == nil {
		png.Encode(f, composite)
		f.Close()
	}
}
