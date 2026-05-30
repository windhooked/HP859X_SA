// Command sweeprun boots the HP 8593A with continuous IRQ6 + synthetic ADC
// stimulus woven into the main loop (not as a post-boot burst). The goal is
// to drive the firmware through enough sweep cycles that it processes the
// sweep-done flag (FFBEFA bit 13), resets the trace buffer, and renders the
// captured trace to screen.
//
// Usage:
//
//	go run ./cmd/sweeprun/ [cycles] [out.png]   # default 200M cycles, screens/sweep_run.png
package main

import (
	"fmt"
	"image/png"
	"os"
	"sort"
	"strconv"

	"github.com/windhooked/HP859X_SA/internal/emutest"
	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
	"github.com/windhooked/HP859X_SA/pkg/emu/machine"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

func main() {
	totalCycles := 200_000_000
	out := "screens/sweep_run.png"
	if len(os.Args) > 1 {
		if n, err := strconv.Atoi(os.Args[1]); err == nil && n > 0 {
			totalCycles = n
		}
	}
	if len(os.Args) > 2 {
		out = os.Args[2]
	}

	romDir := "hp8593a_eeproms"
	if v := os.Getenv("ROM_DIR"); v != "" {
		romDir = v
	}
	rom, err := romloader.LoadDir(romDir)
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

	// Mirror the canonical BootToOperating loop but ALSO inject IRQ6 (with a
	// synthetic ADC sample at 0xFFF200) once the firmware has armed the sweep
	// (FFBF34 == 0x40B8, the capture handler).
	const (
		chunkCycles     = 2000
		breakThresh     = 50
		irq5Period      = 5 // every 5 chunks, fire IRQ5 (timer)
		irq6PeriodArmed = 8 // every 8 chunks, fire IRQ6 (sample) — once sweep armed
		irqServiceCost  = 400
	)
	lb := emutest.NewLoopBreaker(breakThresh)

	rdBF34 := func() uint32 { return m.Bus.Read(0xFFBF34, bus.Long) }
	rd := func(a uint32) uint32 { return m.Bus.Read(a, bus.Long) }
	rdw := func(a uint32) uint32 { return m.Bus.Read(a, bus.Word) }

	// disableLBPostBoot: once the firmware reaches the operating loop, the
	// boot-time LoopBreaker becomes harmful — if the firmware re-runs its
	// non-destructive march RAM test (0x4784) over live RAM, forcing A2 to the
	// end skips the per-cell RESTORE and leaves the 0xFFBFxx timer/sweep state
	// stuck at the 0x5555 march pattern. Set SWEEP_DISABLE_LB=1 to switch the
	// breaker off after the sweep first arms and observe whether the corruption
	// disappears.
	disableLBPostBoot := os.Getenv("SWEEP_DISABLE_LB") == "1"
	// protectSelfTest: once booted, treat the POST self-test region (CPU/ROM
	// checksum 0x44xx–0x456A + march RAM test 0x4770–0x47F6) as atomic — don't
	// break its loops and don't inject IRQs while PC is inside it, so the march
	// test's save→invert→restore cycle over live RAM stays balanced (matching
	// real HW, which runs the test with interrupts masked).
	protectSelfTest := os.Getenv("SWEEP_PROTECT_SELFTEST") == "1"
	inSelfTest := func(pc uint32) bool { return pc >= 0x4490 && pc <= 0x47F6 }

	sweepArmedAt := -1
	samplePos := 0
	irq6Count := 0

	marchHitsPostArm := 0 // chunks with PC in the march loop after the sweep armed
	lbBreaksPostArm := 0  // LoopBreaker fires after the sweep armed
	corruptAt := -1       // chunk at which bf30 first reads 0x55555555
	corruptPC := uint32(0)
	pcHist := map[uint32]int{} // PC >> 8 histogram, sampled post-arm
	resetHits, supEnterHits, checksumHits := 0, 0, 0
	postCaptured := false

	for done := 0; done < totalCycles; done += chunkCycles {
		m.CPU.Run(chunkCycles)
		pc := m.CPU.Reg(cpu.PC)
		chunk := done / chunkCycles

		booted := sweepArmedAt != -1
		// One-shot: capture the call context the first time POST (re)starts
		// post-arm. PC==0x4406 is the checksum routine entry — capture
		// CPU register self-test. Dump the stack so we can see who invoked POST.
		if booted && !postCaptured && pc == 0x4776 {
			postCaptured = true
			fmt.Printf("MARCH-fill entered post-arm at chunk %d: A0=%08X A1=%08X A2=%08X D0=%08X SR=%04X\n",
				chunk, m.CPU.Reg(cpu.A0), m.CPU.Reg(cpu.A1), m.CPU.Reg(cpu.A2),
				m.CPU.Reg(cpu.D0), m.CPU.Reg(cpu.SR))
		}
		protect := protectSelfTest && booted && inSelfTest(pc)
		lbActive := !(disableLBPostBoot && booted) && !protect
		if lbActive {
			if lb.Check(pc, m.CPU.SetReg) && booted {
				lbBreaksPostArm++
			}
		}
		if pc >= 0x4784 && pc <= 0x47F6 && sweepArmedAt != -1 {
			marchHitsPostArm++
		}
		if sweepArmedAt != -1 {
			if pc >= 0x3A9E && pc <= 0x3AAC { // IRQ7/NMI handler entry (soft-restart)
				resetHits++ // nmiEntryHits
			}
			if pc == 0x3AC2 || pc == 0x39BC { // POST call sites (NMI-path / boot)
				supEnterHits++ // postCallHits
			}
			if pc >= 0x454A && pc <= 0x456A { // ROM checksum inner loop
				checksumHits++
			}
		}
		if sweepArmedAt != -1 {
			pcHist[pc>>8]++
		}
		bf30v := rd(0xFFBF30)
		if booted && corruptAt == -1 && (bf30v == 0x55555555 || bf30v == 0xAAAAAAAA) {
			corruptAt = chunk
			corruptPC = pc
			fmt.Printf("bf30 corrupted to 0x55555555 at chunk %d (PC=%06X) "+
				"[marchHitsPostArm=%d lbBreaksPostArm=%d]\n",
				chunk, pc, marchHitsPostArm, lbBreaksPostArm)
		}

		// IRQ5 timer tick — same cadence as BootToOperating. Suppressed while the
		// firmware is inside the atomic POST self-test (protect mode).
		if chunk%irq5Period == 0 && !protect {
			m.CPU.SetIRQ(5)
			m.CPU.Run(irqServiceCost)
			m.CPU.SetIRQ(0)
		}

		// IRQ6 sample capture — only once the firmware has armed the sweep.
		armed := rdBF34() == 0x40B8
		if armed && sweepArmedAt == -1 {
			sweepArmedAt = chunk
			fmt.Printf("sweep armed at chunk %d (~%d cycles); bf30=%08X bf34=%08X\n",
				chunk, chunk*chunkCycles, rd(0xFFBF30), rdBF34())
		}
		if armed && chunk%irq6PeriodArmed == 0 {
			// Synthetic ADC sample: noise floor + peak at sample position 200.
			v := uint32(0x0140)
			d := samplePos - 200
			if d < 0 {
				d = -d
			}
			if d < 25 {
				v += uint32(25-d) * 0x60
			}
			m.Bus.Write(0xFFF200, bus.Word, v)
			samplePos = (samplePos + 1) % 401
			m.CPU.SetIRQ(6)
			m.CPU.Run(irqServiceCost)
			m.CPU.SetIRQ(0)
			irq6Count++
		}
	}

	d := m.MMIO.Display
	fmt.Printf("\n=== Result ===\n")
	fmt.Printf("  total cycles=%d  IRQ6 ticks=%d  sweep armed at chunk %d\n",
		totalCycles, irq6Count, sweepArmedAt)
	fmt.Printf("  final PC=%06X  SR=%04X  A5=%08X\n",
		m.CPU.Reg(cpu.PC), m.CPU.Reg(cpu.SR), m.CPU.Reg(cpu.A5))
	fmt.Printf("  bf30=%08X bf34=%08X befa=%04X\n",
		rd(0xFFBF30), rdBF34(), rdw(0xFFBEFA))
	fmt.Printf("  corruptAt=chunk %d (PC=%06X)  marchHitsPostArm=%d  lbBreaksPostArm=%d  disableLBPostBoot=%v\n",
		corruptAt, corruptPC, marchHitsPostArm, lbBreaksPostArm, disableLBPostBoot)
	fmt.Printf("  nmiEntryHits(0x3A9E)=%d  postCallHits(0x3AC2/0x39BC)=%d  checksumHits=%d\n",
		resetHits, supEnterHits, checksumHits)
	// Top PC pages (>>8) sampled post-arm — reveals whether the firmware is in
	// the operating loop (0x185xx/0x5Exxx) or boot-looping (0x045xx/0x047xx/0x0D7xx).
	type pg struct {
		page  uint32
		count int
	}
	var pages []pg
	for p, c := range pcHist {
		pages = append(pages, pg{p, c})
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i].count > pages[j].count })
	fmt.Printf("  top post-arm PC pages:")
	for i := 0; i < len(pages) && i < 12; i++ {
		fmt.Printf(" %04X:%d", pages[i].page, pages[i].count)
	}
	fmt.Println()
	fmt.Printf("  draw counts: moves=%d glyphs=%d lines=%d rects=%d dots=%d paints=%d paintWords=%d\n",
		d.Moves, d.Glyphs, d.Lines, d.Rects, d.Dots, d.Paints, d.PaintWords)
	// VRAM pattern check: dump 24 bytes of two adjacent rows. If identical →
	// uniform background fill renders as vertical stripes; if they differ → a
	// row-varying (dot) pattern.
	vram := d.Chip.VRAM()
	const rb = 128
	for _, y := range []int{120, 121, 122} {
		base := y * rb
		fmt.Printf("  vram row %d: % 02x\n", y, vram[base:base+24])
	}

	if err := os.MkdirAll("screens", 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	f, err := os.Create(out)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer f.Close()
	if err := png.Encode(f, d.RenderFrame()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("  wrote %s\n", out)
}
