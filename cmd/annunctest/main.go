// Command annunctest: force B070 bit13 clear DURING boot so the oven gate (0x875E)
// takes its remove path — verifies whether that gate controls OVEN COLD (and, if
// so, clears it the faithful way: the firmware's own check removes it).
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
	mode := "b070"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}
	rom, _ := romloader.LoadDir("hp8593a_eeproms")
	m, _ := machine.New8593A(rom)
	m.CPU.Reset()
	lb := emutest.NewLoopBreaker(50)
	for c := 0; c < 165_000_000; c += 2000 {
		switch mode {
		case "b070": // force oven-cold hw flag (B070 bit13) clear = "warm"
			m.Bus.Write(0xFFB070, bus.Word, m.Bus.Read(0xFFB070, bus.Word)&^0x2000)
		case "b078": // force the state index (B078 bits[7:4]) to 0 (=> fcn.79CC 9000 >= 300)
			m.Bus.Write(0xFFB078, bus.Word, m.Bus.Read(0xFFB078, bus.Word)&^0x00F0)
		}
		m.CPU.Run(2000)
		lb.Check(m.CPU.Reg(cpu.PC), m.CPU.SetReg)
		if (c/2000)%5 == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Run(400)
			m.CPU.SetIRQ(0)
		}
	}
	out := "screens/oven_" + mode + ".png"
	f, _ := os.Create(out)
	png.Encode(f, m.MMIO.Display.RenderFrame())
	f.Close()
	fmt.Printf("mode=%s wrote %s\n", mode, out)
}
