// Command anndesc dumps the annunciator descriptor table [0xFF9562] and maps
// each code to its string, to get the VERIFIED code for OVEN COLD / REF UNLOCK /
// ADC-TIME (string base 0x2b31e). fcn.e7f0 indexes base+code*6.
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
	base := m.Bus.Read(0xFF9562, bus.Long)
	fmt.Printf("descriptor table base [0xFF9562] = %06X\n", base)
	rdStr := func(a uint32) string {
		s := ""
		for i := 0; i < 16; i++ {
			c := byte(m.Bus.Read(a+uint32(i), bus.Byte))
			if c < 0x20 || c > 0x7e {
				break
			}
			s += string(c)
		}
		return s
	}
	for code := 0; code <= 0x40; code++ {
		d := base + uint32(code)*6
		w0 := m.Bus.Read(d, bus.Word)
		w1 := m.Bus.Read(d+2, bus.Word)
		w2 := m.Bus.Read(d+4, bus.Word)
		var str string
		for _, w := range []uint32{w0, w1, w2} {
			if s := rdStr(0x2b31e + (w & 0xFFFF)); len(s) >= 3 {
				str = s
				break
			}
		}
		fmt.Printf("  code 0x%02X  [%04X %04X %04X]  str=%q\n", code, w0, w1, w2, str)
	}
}
