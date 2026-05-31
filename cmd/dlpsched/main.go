// dlpsched (phase 11): with both gates forced + IRQ6 driving, check the sweep-
// completion chain: befa bit13 (sweep-done) sets? sweep-trace DLP state machine
// 0x5ECEE / scheduler 0x5ED7E reached? IRQ6 capture handler 0x4088/0x40B8 runs?
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
	var hit5ECEE, hit5ED7E, hit5ED04, irq6cap, befa13sets int
	var lastBefa uint16
	m.Bus.OnRead = func(a uint32, s bus.Size, v uint32) {
		switch a {
		case 0x05ECEE: hit5ECEE++
		case 0x05ED7E: hit5ED7E++
		case 0x05ED04: hit5ED04++
		case 0x004088, 0x0040B8, 0x00410A: irq6cap++
		}
	}
	m.Bus.OnWrite = func(a uint32, s bus.Size, v uint32) {
		if a == 0xFFBEFA {
			nb := uint16(v)
			if nb&0x2000 != 0 && lastBefa&0x2000 == 0 { befa13sets++ }
			lastBefa = nb
		}
	}
	m.BootToOperating(190_000_000)
	hit5ECEE, hit5ED7E, hit5ED04, irq6cap, befa13sets = 0,0,0,0,0
	m.MMIO.SweepActive = true
	for i := 0; i < 8_000_000; i++ {
		if uint8(m.Bus.Read(0xFFB0EC, bus.Byte)) != 0x31 { m.Bus.Write(0xFFB0EC, bus.Byte, 0x31) }
		if int16(m.Bus.Read(0xFFA9A0, bus.Word)) < 0 { m.Bus.Write(0xFFA9A0, bus.Word, 0x0080) }
		if m.CPU.Step() != nil { break }
		if i%2000 == 0 { m.CPU.SetIRQ(5); m.CPU.Step(); m.CPU.SetIRQ(0) }
		if i%1200 == 0 { m.CPU.SetIRQ(6); m.CPU.Step(); m.CPU.SetIRQ(0) }
	}
	befa := uint16(m.Bus.Read(0xFFBEFA, bus.Word))
	bf34 := uint16(m.Bus.Read(0xFFBF34, bus.Word))
	a5 := m.CPU.Reg(cpu.A5)
	bf30 := m.Bus.Read(0xFFBF30, bus.Long)
	fmt.Printf("IRQ6 capture handler hits: %d\n", irq6cap)
	fmt.Printf("befa bit13 (sweep-done) set events: %d   final befa=%04X (bit13=%d)\n", befa13sets, befa, (befa>>13)&1)
	fmt.Printf("bf34 (IRQ6 dispatch)=%04X  A5=%06X bf30=%06X (A5>=bf30: %v)\n", bf34, a5, bf30, uint32(a5)>=bf30)
	fmt.Printf("sweep-trace DLP: 0x5ECEE x%d  0x5ED04 x%d  0x5ED7E(sched) x%d\n", hit5ECEE, hit5ED04, hit5ED7E)
}
