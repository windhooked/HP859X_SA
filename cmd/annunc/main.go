// Command annunc finds the annunciator status words. The annunciator drawer
// (~ROM 0x260C8) tests bits of status longwords at fixed offsets from a base
// pointer a4 (btst.l d0,0x78(a4); 0xA0(a4); 0xAA(a4); 0xBE(a4)). This boots to
// the live UI state, captures a4 when the drawer runs, dumps those status
// longwords + their set bits, and renders the screen to ./screens/. Set bits =
// active annunciators (REF UNLOCK / OVEN COLD / ADC-* FAIL etc.).
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
	// Interleave short single-step bursts during the whole boot to catch the
	// annunciator drawer (PC in 0x26000..0x2611C) whenever it runs; capture a4.
	var a4 uint32
	catch := func() bool {
		for s := 0; s < 400; s++ {
			pc := m.CPU.Reg(cpu.PC)
			if pc >= 0x26000 && pc <= 0x2611C {
				a4 = m.CPU.Reg(cpu.A4)
				return true
			}
			if m.CPU.Step() != nil {
				return false
			}
		}
		return false
	}
	for done := 0; done < 200_000_000 && a4 == 0; done += 2000 {
		if catch() {
			break
		}
		m.CPU.Run(2000)
		lb.Check(m.CPU.Reg(cpu.PC), m.CPU.SetReg)
		if (done/2000)%5 == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Run(400)
			m.CPU.SetIRQ(0)
		}
	}
	if a4 == 0 {
		fmt.Println("drawer not hit")
	} else {
		fmt.Printf("annunciator status base a4 = %08X\n", a4)
		for _, off := range []uint32{0x78, 0xA0, 0xAA, 0xBE} {
			v := m.Bus.Read(a4+off, bus.Long)
			fmt.Printf("  [a4+%02X] = %08X  set bits:", off, v)
			for b := 0; b < 32; b++ {
				if v&(1<<b) != 0 {
					fmt.Printf(" %d", b)
				}
			}
			fmt.Println()
		}
	}
	if f, err := os.Create("screens/annunc_state.png"); err == nil {
		png.Encode(f, m.MMIO.Display.RenderFrame())
		f.Close()
		fmt.Println("wrote screens/annunc_state.png")
	}
}
