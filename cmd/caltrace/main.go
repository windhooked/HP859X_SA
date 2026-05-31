// Command caltrace single-steps the boot ADC PRESET cal (fcn.5E6E8 + its
// EOC-wait fcn.5E5DE) and logs, for the FIRST few cal executions, exactly where
// it passes or fails: each EOC-wait outcome (matched vs 1000-poll timeout), each
// ADC result read, and which $94da=0xFFFF fail-write site is hit. This shows
// whether the ADC cal fails on TIMING (EOC never asserts in budget) or on a
// VALUE check downstream — i.e. whether the model is derivable from the firmware
// alone.
package main

import (
	"fmt"
	"image/png"
	"os"

	"github.com/windhooked/HP859X_SA/internal/emutest"
	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
	"github.com/windhooked/HP859X_SA/pkg/emu/machine"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

func main() {
	rom, _ := romloader.LoadDir("hp8593a_eeproms")
	m, _ := machine.New8593A(rom)
	m.CPU.Reset()
	lb := emutest.NewLoopBreaker(50)
	rdw := func(a uint32) uint16 { return uint16(m.Bus.Read(a, bus.Word)) }

	// Boot until the cal step fcn.5E6E8 is first entered.
	entered := false
	// Boot to ~15M with Run (fast), then pure single-step to catch fcn.5E6E8
	// (the cal runs ~22-31M cycles when $94dd enables).
	for done := 0; done < 15_000_000; done += 2000 {
		m.CPU.Run(2000)
		lb.Check(m.CPU.Reg(cpu.PC), m.CPU.SetReg)
		if (done/2000)%5 == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Run(400)
			m.CPU.SetIRQ(0)
		}
	}
	for s := 0; s < 60_000_000; s++ {
		if m.CPU.Reg(cpu.PC) == 0x5E6E8 {
			entered = true
			break
		}
		if m.CPU.Step() != nil {
			break
		}
		if s%2500 == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Step()
			m.CPU.SetIRQ(0)
		}
	}
	if !entered {
		fmt.Println("cal fcn.5E6E8 never entered; 94dd=", m.Bus.Read(0xFF94DD, bus.Byte))
		return
	}
	rdl := func(a uint32) uint32 { return m.Bus.Read(a, bus.Long) }
	fmt.Printf("at cal entry: 948e(cal-data ptr)=%08X  bb4e(src ptr)=%08X  94e4=%04X 94da=%04X\n",
		rdl(0xFF948E), rdl(0xFFBB4E), rdw(0xFF94E4), rdw(0xFF94DA))

	// Track reachability of the cal-pass chain over a long run, and whether the
	// $948e cal-data pointer ever becomes non-null.
	reach := map[uint32]int{
		0x3760:  0, // move.l $bb4e,$948e  (sets the cal-data pointer)
		0x5EFC0: 0, // cal-validate routine entry
		0x5EFE0: 0, // tst.l $948e gate
		0x5F002: 0, // passed the $948e!=0 gate (clears $94e4 then validates)
		0x5F046: 0, // move.w #0xD2D2,$94e4  (THE pass write)
	}
	var firstNonNull948e uint32
	for i := 0; i < 6_000_000; i++ {
		pc := m.CPU.Reg(cpu.PC)
		if _, ok := reach[pc]; ok {
			reach[pc]++
		}
		if firstNonNull948e == 0 && rdl(0xFF948E) != 0 {
			firstNonNull948e = rdl(0xFF948E)
		}
		if m.CPU.Step() != nil {
			break
		}
		if i%2500 == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Step()
			m.CPU.SetIRQ(0)
		}
	}
	fmt.Println("reachability over 6M steps:")
	fmt.Printf("  0x3760  (sets $948e from $bb4e):   %d\n", reach[0x3760])
	fmt.Printf("  0x5EFC0 (validate routine entry):  %d\n", reach[0x5EFC0])
	fmt.Printf("  0x5EFE0 ($948e!=0 gate):           %d\n", reach[0x5EFE0])
	fmt.Printf("  0x5F002 (passed gate):             %d\n", reach[0x5F002])
	fmt.Printf("  0x5F046 (writes 94e4=D2D2 PASS):   %d\n", reach[0x5F046])
	fmt.Printf("  $948e ever non-null: %08X   final 94e4=%04X\n", firstNonNull948e, rdw(0xFF94E4))

	if f, err := os.Create("screens/caltrace_boot.png"); err == nil {
		png.Encode(f, m.MMIO.Display.RenderFrame())
		f.Close()
		fmt.Println("wrote screens/caltrace_boot.png")
	}
}
