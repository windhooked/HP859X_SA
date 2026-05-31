// Command longrun tests whether the boot DLP personality-init is a slow but
// PROGRESSING load (declaring hundreds of measurement variables, each a record
// search) versus a true infinite loop. It boots, then runs long windows,
// reporting per window: display Lines (trace draws => big jump), whether the C
// operating loop fcn.18568 is reached, $b0a0 bit11, and the DLP record table
// count $bfe6 (growing => progress). Drives IRQ5 + periodic IRQ1/IRQ6 sweep.
package main

import (
	"fmt"
	"image/png"
	"os"

	"github.com/windhooked/HP859X_SA/internal/emutest"
	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
	"github.com/windhooked/HP859X_SA/pkg/emu/device"
	"github.com/windhooked/HP859X_SA/pkg/emu/machine"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

func main() {
	rom, _ := romloader.LoadDir("hp8593a_eeproms")
	m, _ := machine.New8593A(rom)
	m.CPU.Reset()
	lb := emutest.NewLoopBreaker(50)
	rdL := func(a uint32) uint32 { return m.Bus.Read(a, bus.Long) }
	rdW := func(a uint32) uint16 { return uint16(m.Bus.Read(a, bus.Word)) }

	force94e4 := os.Getenv("FORCE_94E4") != "" // force the cal-validated sentinel
	const windowCycles = 25_000_000
	for w := 0; w < 16; w++ {
		reached := false
		for done := 0; done < windowCycles; done += 2000 {
			if force94e4 {
				m.Bus.Write(0xFF94E4, bus.Word, 0xD2D2)
				m.Bus.Write(0xFF94DA, bus.Word, 0x0000) // clear the ADC-fail marker
			}
			// brief single-step burst to sample for fcn.18568
			for s := 0; s < 6; s++ {
				if m.CPU.Step() != nil {
					break
				}
				pc := m.CPU.Reg(cpu.PC)
				if pc >= 0x18560 && pc < 0x18B00 {
					reached = true
				}
			}
			m.CPU.Run(2000)
			lb.Check(m.CPU.Reg(cpu.PC), m.CPU.SetReg)
			if (done/2000)%5 == 0 {
				m.CPU.SetIRQ(5)
				m.CPU.Run(400)
				m.CPU.SetIRQ(0)
			}
			// periodic sweep step+capture
			if (done/2000)%11 == 0 && rdL(0xFFBF34) == 0x40B8 && m.CPU.Reg(cpu.A5) < rdL(0xFFBF30) {
				m.CPU.SetIRQ(1)
				m.CPU.Run(250)
				m.CPU.SetIRQ(0)
				m.Bus.Write(0xFFF200, bus.Word, 0x0180)
				m.CPU.SetIRQ(6)
				m.CPU.Run(250)
				m.CPU.SetIRQ(0)
			}
		}
		fmt.Printf("window %2d (~%dM cyc): Lines=%d Dots=%d Glyphs=%d  opLoop=%v  b0a0=%04X  bfe6=%d a62a=%04X\n",
			w, (w+1)*25, m.MMIO.Display.Lines, m.MMIO.Display.Dots, m.MMIO.Display.Glyphs,
			reached, rdW(0xFFB0A0), rdW(0xFFBFE6), rdW(0xFFA62A))
	}

	if len(device.A7ReadHist) > 0 {
		fmt.Println("A7 register reads over the whole boot (reg -> count, lastSelect, lastVal):")
		for r := 0; r < 16; r++ {
			if v, ok := device.A7ReadHist[r]; ok {
				fmt.Printf("  reg %2d: count=%-7d lastSel=%04X lastVal=%04X\n", r, v[0], v[1], v[2])
			}
		}
	}

	// Render the final framebuffer so we can see what the (now un-frozen)
	// state machine drew.
	out := "screens/longrun.png"
	if force94e4 {
		out = "screens/longrun_force94e4.png"
	}
	if f, err := os.Create(out); err == nil {
		png.Encode(f, m.MMIO.Display.RenderFrame())
		f.Close()
		fmt.Println("wrote", out)
	}

	// Capture what the frozen loop does with the A7 analog-interface I/O bus:
	// the select word written to 0xFFF728 (high byte = A7 register address) and
	// the data read back from 0xFFF72A. Also tally the conditional branches'
	// flag sources.
	fmt.Println("\n--- A7 I/O-bus activity in the frozen loop (40k steps) ---")
	selHist := map[uint16]int{}  // high byte of select word -> A7 register addr
	readHist := map[uint16]int{} // 0xFFF72A readback values
	for s := 0; s < 40000; s++ {
		pc := m.CPU.Reg(cpu.PC)
		if pc == 0x2265C { // move.w d6,0xf728 : d6 = select word
			selHist[uint16(m.CPU.Reg(cpu.D6))>>8]++
		}
		if m.CPU.Step() != nil {
			break
		}
		if pc == 0x22660 { // just executed move.w 0xf72a,d0 : d0 = readback
			readHist[uint16(m.CPU.Reg(cpu.D0))]++
		}
	}
	fmt.Println("A7 register selects (high byte of 0xFFF728) -> count:")
	for sel, n := range selHist {
		fmt.Printf("  reg %02X x%d\n", sel, n)
	}
	fmt.Println("0xFFF72A readback values -> count:")
	for v, n := range readHist {
		fmt.Printf("  %04X x%d\n", v, n)
	}

	// Dump the frozen idle loop: distinct PCs, then the loop body disassembled.
	fmt.Println("\n--- frozen idle loop (200 steps) ---")
	seen := map[uint32]bool{}
	lo, hi := uint32(0xFFFFFFFF), uint32(0)
	for s := 0; s < 200; s++ {
		pc := m.CPU.Reg(cpu.PC)
		seen[pc] = true
		if pc < lo {
			lo = pc
		}
		if pc > hi {
			hi = pc
		}
		if m.CPU.Step() != nil {
			break
		}
	}
	fmt.Printf("loop spans %06X..%06X (%d distinct PCs)\n", lo, hi, len(seen))
	if hi-lo < 0x200 {
		for a := lo; a <= hi; {
			d, n := m.CPU.Disasm(a)
			mark := "   "
			if seen[a] {
				mark = ">> "
			}
			fmt.Printf("  %s%06X  %s\n", mark, a, d)
			if n == 0 {
				n = 2
			}
			a += n
		}
	}
}
