package main

import (
	"fmt"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/machine"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

func main() {
	rom, _ := romloader.LoadDir("hp8593a_eeproms")
	m, _ := machine.New8593A(rom)
	m.CPU.Reset()
	m.BootToOperating(165_000_000)
	names := map[int]string{0x0B: "ADC-GND", 0x0D: "OVEN-COLD", 0x18: "ADC-2V", 0x23: "FREQ-UNCAL", 0x28: "ADC-TIME"}
	for _, base := range []uint32{0xFFB060, 0xFFB068, 0xFFB08C, 0xFFB098, 0xFFB084} {
		var bits uint64
		for w := 0; w < 4; w++ {
			bits |= uint64(uint16(m.Bus.Read(base+uint32(w*2), bus.Word))) << (uint(w) * 16)
		}
		fmt.Printf("%06X = %016X  set codes:", base, bits)
		for b := 0; b < 64; b++ {
			if bits&(1<<uint(b)) != 0 {
				if n, ok := names[b]; ok {
					fmt.Printf(" 0x%02X(%s)", b, n)
				} else {
					fmt.Printf(" 0x%02X", b)
				}
			}
		}
		fmt.Println()
	}
	for _, a := range []uint32{0xFFB1F0, 0xFFB1F6, 0xFFB1FA, 0xFFB1F8, 0xFFB1E0} {
		fmt.Printf("source %06X = %04X\n", a, uint16(m.Bus.Read(a, bus.Word)))
	}
}
