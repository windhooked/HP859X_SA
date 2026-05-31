// Command tracedraw probes the DLP trace-DISPLAY path: it boots to the
// operating loop, drives a sweep (synthetic detector samples via IRQ6, gated on
// A5<bf30), then captures EVERY drawLine the firmware emits over a display
// window and classifies them. A spectrum/zero-span trace shows up as a dense
// run of short, non-axis-aligned segments inside the graticule; the graticule
// itself is a handful of long axis-aligned lines at fixed pitch. This tells us
// whether the firmware emits trace-draw commands at all (and with what
// coordinates) — the crux of why Lines stays ~77.
//
//	DYLD_FALLBACK_LIBRARY_PATH=/usr/local/lib go run ./cmd/tracedraw/
package main

import (
	"fmt"
	"os"
	"sort"

	"github.com/windhooked/HP859X_SA/internal/emutest"
	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
	"github.com/windhooked/HP859X_SA/pkg/emu/device/hd63484"
	"github.com/windhooked/HP859X_SA/pkg/emu/machine"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

func main() {
	rom, _ := romloader.LoadDir("hp8593a_eeproms")
	m, _ := machine.New8593A(rom)
	m.CPU.Reset()
	lb := emutest.NewLoopBreaker(50)
	rdL := func(a uint32) uint32 { return m.Bus.Read(a, bus.Long) }

	// Boot to the armed-sweep state.
	armed := false
	for done := 0; done < 60_000_000 && !armed; done += 2000 {
		m.CPU.Run(2000)
		lb.Check(m.CPU.Reg(cpu.PC), m.CPU.SetReg)
		if (done/2000)%5 == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Run(400)
			m.CPU.SetIRQ(0)
		}
		if rdL(0xFFBF34) == 0x40B8 {
			armed = true
		}
	}
	fmt.Printf("armed=%v A5=%08X bf30=%08X bf34=%08X\n", armed, m.CPU.Reg(cpu.A5), rdL(0xFFBF30), rdL(0xFFBF34))

	// Fill a sweep: synthetic samples shaped like a peak so a real trace would
	// be visibly non-flat (sample = noise floor + a Gaussian-ish bump).
	bf30 := rdL(0xFFBF30)
	for n := 0; n < 1000 && m.CPU.Reg(cpu.A5) < bf30; n++ {
		v := 0x0140
		d := n%401 - 200
		if d < 0 {
			d = -d
		}
		v += (200 - d) * 2 // peak in the middle
		m.Bus.Write(0xFFF200, bus.Word, uint32(v))
		m.CPU.SetIRQ(6)
		m.CPU.Run(400)
		m.CPU.SetIRQ(0)
		m.CPU.Run(2000)
	}
	befa := m.Bus.Read(0xFFBEFA, bus.Word)
	fmt.Printf("sweep filled: A5=%08X befa=%04X (done-bit13=%v)\n", m.CPU.Reg(cpu.A5), befa, befa&0x2000 != 0)

	// Snapshot counters, then run a display window, sampling PC pages to see
	// where the firmware actually spends time (is it in the operating loop /
	// display-update at all?).
	d := m.MMIO.Display
	c0 := [...]int{d.Moves, d.Lines, d.Dots, d.Glyphs, d.Paints, d.PaintWords}
	m.MMIO.Display.EnableLineLog()
	forceGate := os.Getenv("FORCE_GATE") != ""
	pcHist := map[uint32]int{}
	reachedOpLoop := false
	for i := 0; i < 4000; i++ {
		for s := 0; s < 200; s++ { // sample PC by single-stepping a slice
			if m.CPU.Step() != nil {
				break
			}
			pc := m.CPU.Reg(cpu.PC)
			pcHist[pc>>10]++ // 1KB-page histogram
			if pc >= 0x18560 && pc < 0x18B00 {
				reachedOpLoop = true
			}
			if forceGate { // DECISIVE TEST: keep the trace-draw busy gate clear
				v := m.Bus.Read(0xFFB0A0, bus.Word)
				if v&0x0800 != 0 {
					m.Bus.Write(0xFFB0A0, bus.Word, v&^uint32(0x0800))
				}
			}
		}
		m.CPU.Run(2000)
		lb.Check(m.CPU.Reg(cpu.PC), m.CPU.SetReg)
		if i%5 == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Run(400)
			m.CPU.SetIRQ(0)
		}
		// Drive a full sweep the way the hardware does: IRQ1 steps the sweep
		// (advances frequency + programs LO/IF DACs + sets befa bit13), IRQ6
		// captures the ADC sample. Interleave the two for each point until the
		// buffer fills, then let the firmware finalise/draw.
		for n := 0; n < 450 && m.CPU.Reg(cpu.A5) < rdL(0xFFBF30); n++ {
			m.CPU.SetIRQ(1) // sweep step
			m.CPU.Run(300)
			m.CPU.SetIRQ(0)
			m.Bus.Write(0xFFF200, bus.Word, uint32(0x0140+(n%200)))
			m.CPU.SetIRQ(6) // sample capture
			m.CPU.Run(300)
			m.CPU.SetIRQ(0)
		}
	}
	fmt.Printf("reached operating loop (0x18568) during window: %v\n", reachedOpLoop)
	rdw := func(a uint32) uint16 { return uint16(m.Bus.Read(a, bus.Word)) }
	fmt.Printf("trace-draw gate cells: 9fb4=%04X(need>1) b0a0=%04X(need bit11=0) adc4=%04X(bit15) a9a0=%04X(need>=0)\n",
		rdw(0xFF9FB4), rdw(0xFFB0A0), rdw(0xFFADC4), rdw(0xFFA9A0))
	c1 := [...]int{d.Moves, d.Lines, d.Dots, d.Glyphs, d.Paints, d.PaintWords}
	names := [...]string{"Moves", "Lines", "Dots", "Glyphs", "Paints", "PaintWords"}
	fmt.Println("draw-counter deltas over window:")
	for i := range names {
		fmt.Printf("  %-10s %d -> %d  (+%d)\n", names[i], c0[i], c1[i], c1[i]-c0[i])
	}
	type pe struct {
		p uint32
		n int
	}
	var pes []pe
	for p, n := range pcHist {
		pes = append(pes, pe{p, n})
	}
	sort.Slice(pes, func(i, j int) bool { return pes[i].n > pes[j].n })
	fmt.Println("top PC 1KB-pages during window (range : samples):")
	for i, e := range pes {
		if i >= 14 {
			break
		}
		fmt.Printf("  %06X-%06X : %d\n", e.p<<10, (e.p<<10)+0x3FF, e.n)
	}

	ll := m.MMIO.Display.LineLog
	var axis, diag int
	histLen := map[int]int{}
	var sample []hd63484.LineRec
	for _, r := range ll {
		dx, dy := r.X1-r.X0, r.Y1-r.Y0
		if dx < 0 {
			dx = -dx
		}
		if dy < 0 {
			dy = -dy
		}
		L := dx
		if dy > L {
			L = dy
		}
		histLen[L]++
		if dx == 0 || dy == 0 {
			axis++
		} else {
			diag++
			if len(sample) < 30 {
				sample = append(sample, r)
			}
		}
	}
	fmt.Printf("\ncaptured %d drawLine calls: %d axis-aligned, %d diagonal\n", len(ll), axis, diag)
	// Length histogram (top entries) — a trace = many segments of length 1..a few.
	type lc struct{ L, n int }
	var lcs []lc
	for L, n := range histLen {
		lcs = append(lcs, lc{L, n})
	}
	sort.Slice(lcs, func(i, j int) bool { return lcs[i].n > lcs[j].n })
	fmt.Println("line-length histogram (len:count, by frequency):")
	for i, e := range lcs {
		if i >= 12 {
			break
		}
		fmt.Printf("  len=%-4d count=%d\n", e.L, e.n)
	}
	if len(sample) > 0 {
		fmt.Println("sample diagonal segments (trace candidates):")
		for _, r := range sample {
			fmt.Printf("  (%d,%d)->(%d,%d)\n", r.X0, r.Y0, r.X1, r.Y1)
		}
	}
	_ = os.Stdout
}
