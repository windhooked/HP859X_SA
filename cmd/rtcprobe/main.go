// rtcprobe demonstrates the modeled hardware RTC end-to-end: it boots the
// instrument, then directly invokes the firmware's own RTC-read routine
// (ROM fcn.59E2C) and decodes the BCD date/time that routine read back from the
// device.FrontPanel clock registers (0xEF4001..0xEF4017). This proves the
// firmware sees a correct, settable real-time clock.
//
// fcn.59E2C returns the 6 packed BCD bytes in d0:d1 —
//
//	d0 low word = (year<<8)|month ; d1 = (day<<24)|(hour<<16)|(min<<8)|sec.
package main

import (
	"fmt"
	"time"

	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
	"github.com/windhooked/HP859X_SA/pkg/emu/machine"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

const (
	rtcReadFn = 0x59E2C
	sentinel  = 0xFFEFFE // return address we stop on (mapped RAM)
)

func callRTCRead(m *machine.Machine) (d0, d1 uint32) {
	sp := m.CPU.Reg(cpu.A7) - 4
	m.Bus.Write(sp, 4, sentinel) // push return address
	m.CPU.SetReg(cpu.A7, sp)
	m.CPU.SetReg(cpu.PC, rtcReadFn)
	for i := 0; i < 200; i++ {
		if _, hit := m.CPU.RunUntil(200000, sentinel); hit {
			break
		}
	}
	return m.CPU.Reg(cpu.D0), m.CPU.Reg(cpu.D1)
}

func decode(d0, d1 uint32) string {
	yr := (d0 >> 8) & 0xFF
	mo := d0 & 0xFF
	day := (d1 >> 24) & 0xFF
	hr := (d1 >> 16) & 0xFF
	mi := (d1 >> 8) & 0xFF
	se := d1 & 0xFF
	return fmt.Sprintf("20%02X-%02X-%02X %02X:%02X:%02X (BCD)", yr, mo, day, hr, mi, se)
}

func main() {
	rom, _ := romloader.LoadDir("hp8593a_eeproms")
	m, _ := machine.New8593A(rom)
	m.CPU.Reset()
	m.BootToOperating(40_000_000)

	fmt.Println("=== default RTC (power-up: Rev L build date) ===")
	d0, d1 := callRTCRead(m)
	fmt.Printf("firmware read: %s   [d0=%#08x d1=%#08x]\n", decode(d0, d1), d0, d1)

	fmt.Println("\n=== after SetRTC(2026-06-01 14:37:09) ===")
	m.FrontPanel.SetRTC(time.Date(2026, time.June, 1, 14, 37, 9, 0, time.UTC))
	d0, d1 = callRTCRead(m)
	fmt.Printf("firmware read: %s   [d0=%#08x d1=%#08x]\n", decode(d0, d1), d0, d1)
}
