// Drive one sweep, then dump the trace buffer (0x2FD508, 401 words) to verify the
// SweepEngine spectrum data landed correctly (CAL peak + noise floor), separating
// the data path from the trace-paint (DLP) path.
package main

import (
	"fmt"

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
	m.MMIO.SweepActive = true
	rdL := func(a uint32) uint32 { return m.Bus.Read(a, bus.Long) }
	bufStart := uint32(0x2FD508)
	bf30 := rdL(0xFFBF30)
	fmt.Printf("buffer 0x%06X..0x%06X (%d bytes)\n", bufStart, bf30, bf30-bufStart)
	for m.CPU.Reg(cpu.A5) < bf30 {
		m.CPU.SetIRQ(6)
		m.CPU.Run(250)
		m.CPU.SetIRQ(0)
		if m.CPU.Reg(cpu.A5) == 0 || m.CPU.Reg(cpu.A5) < bufStart {
			break
		}
	}
	fmt.Printf("after fill: A5=%06X\n", m.CPU.Reg(cpu.A5))
	// dump buffer stats
	var mn, mx uint16 = 0xFFFF, 0
	peakPt := -1
	n := int((bf30 - bufStart) / 2)
	for i := 0; i < n; i++ {
		v := uint16(m.Bus.Read(bufStart+uint32(i*2), bus.Word))
		if v < mn {
			mn = v
		}
		if v > mx {
			mx, peakPt = v, i
		}
	}
	fmt.Printf("buffer values: min=%#x max=%#x peak@point=%d (of %d)\n", mn, mx, peakPt, n)
	fmt.Print("sample (every 40th): ")
	for i := 0; i < n; i += 40 {
		fmt.Printf("%#x ", uint16(m.Bus.Read(bufStart+uint32(i*2), bus.Word)))
	}
	fmt.Println()
}
