// Command annunctest: force the 0x875E annunciator-gate (B070 bit13 clear) during
// the operating loop so the firmware's own check calls fcn.e87e(0x31/0x32) to
// REMOVE that annunciator — testing whether 0x875E runs post-boot + which
// annunciator codes 0x31/0x32 are (candidate OVEN COLD).
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
	m.BootToOperating(165_000_000)
	// count whether the oven gate 0x875E executes post-boot
	lb := emutest.NewLoopBreaker(50)
	gateHits := 0
	for c := 0; c < 60_000_000; {
		n, stopped := m.CPU.RunUntil(2000, 0x875E)
		c += n
		if stopped {
			gateHits++
			// force "oven warm": clear B070 bit13 so the gate takes the remove path
			v := m.Bus.Read(0xFFB070, bus.Word) &^ 0x2000
			m.Bus.Write(0xFFB070, bus.Word, v)
			m.CPU.Step()
			continue
		}
		lb.Check(m.CPU.Reg(cpu.PC), m.CPU.SetReg)
		if (c/2000)%5 == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Run(400)
			m.CPU.SetIRQ(0)
		}
	}
	out := "screens/oven_gate_test.png"
	f, _ := os.Create(out)
	png.Encode(f, m.MMIO.Display.RenderFrame())
	f.Close()
	fmt.Printf("0x875E gate executed %d times post-boot; wrote %s\n", gateHits, out)
}
