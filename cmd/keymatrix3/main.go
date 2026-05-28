// Command keymatrix3 — the previous matrix sweep proved the per-key
// DISPATCH path at PC 0x18F5E is gated by bc67.1 + b072.14, and that
// neither bit is set ANYWHERE in the Rev L firmware. So the matrix
// dispatch path is dead code unless something external (the
// front-panel μC writing into RAM directly?) sets these bits.
//
// This tool experimentally pre-arms BOTH bits and a matrix bit, then
// drives the operating tick and checks for IP-witness side effects.
// If the dispatch fires with bc67.1 forced, that PROVES this is the
// path; the question is then what sets bc67.1 in real hardware.
package main

import (
	"fmt"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/machine"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

var ipClearCells = []uint32{
	0xFFAD6C, 0xFFAD6A, 0xFFAD74, 0xFFAD72, 0xFFAD6E, 0xFFAD70,
	0xFFA9AC, 0xFFB0EC, 0xFFB058, 0xFFAD64, 0xFFB20E, 0xFFBA5E,
	0xFFA5D4, 0xFFBF01, 0xFFBAF8,
}

const (
	ramBase = uint32(0xFF0000)
	ramSize = uint32(0x00F000)
)

func snapshot(m *machine.Machine) []byte {
	buf := make([]byte, ramSize)
	for i := uint32(0); i < ramSize; i++ {
		buf[i] = byte(m.Bus.Read(ramBase+i, bus.Byte))
	}
	return buf
}

func main() {
	rom, err := romloader.LoadDir("hp8593a_eeproms")
	if err != nil {
		panic(err)
	}

	// Sweep all 48 bits with bc67.1 and b072.14 forced.
	fmt.Println("[baseline] no key, no force ...")
	mB, _ := machine.New8593A(rom)
	mB.CPU.Reset()
	mB.BootToOperating(30_000_000)
	mB.DriveOperatingTick(20_000_000)
	mB.DriveOperatingTick(20_000_000)
	mB.DriveOperatingTick(20_000_000)
	basePost := snapshot(mB)

	fmt.Println("[1/49] no key, force bc67.1 + b072.14 only ...")
	{
		m, _ := machine.New8593A(rom)
		m.CPU.Reset()
		m.BootToOperating(30_000_000)
		// Force the gate bits.
		v := byte(m.Bus.Read(0xFFBC67, bus.Byte)) | 0x03
		m.Bus.Write(0xFFBC67, bus.Byte, uint32(v))
		v2 := uint32(m.Bus.Read(0xFFB072, bus.Word)) | 0x4000
		m.Bus.Write(0xFFB072, bus.Word, v2)
		// Fire IRQ3 to put matrix-read in motion.
		m.CPU.SetIRQ(3)
		m.CPU.Run(400)
		m.CPU.SetIRQ(0)
		for t := 0; t < 5; t++ {
			m.DriveOperatingTick(10_000_000)
		}
		post := snapshot(m)
		wit := 0
		for _, addr := range ipClearCells {
			off := addr - ramBase
			if basePost[off] != post[off] {
				wit++
			}
		}
		tot := 0
		for i := uint32(0); i < ramSize; i++ {
			if basePost[i] != post[i] {
				tot++
			}
		}
		fmt.Printf("  no-key + bc67.1 + b072.14 forced: %d total, %d witness\n", tot, wit)
	}

	step := 1
	for byteIdx := 0; byteIdx < 6; byteIdx++ {
		for bit := 0; bit < 8; bit++ {
			step++
			m, _ := machine.New8593A(rom)
			m.CPU.Reset()
			m.BootToOperating(30_000_000)
			m.FrontPanel.SetBit(byteIdx, bit)
			// Force gate bits.
			v := byte(m.Bus.Read(0xFFBC67, bus.Byte)) | 0x03
			m.Bus.Write(0xFFBC67, bus.Byte, uint32(v))
			v2 := uint32(m.Bus.Read(0xFFB072, bus.Word)) | 0x4000
			m.Bus.Write(0xFFB072, bus.Word, v2)
			m.CPU.SetIRQ(3)
			m.CPU.Run(400)
			m.CPU.SetIRQ(0)
			for t := 0; t < 5; t++ {
				m.DriveOperatingTick(10_000_000)
			}
			post := snapshot(m)

			wit := 0
			var witCells []uint32
			for _, addr := range ipClearCells {
				off := addr - ramBase
				if basePost[off] != post[off] {
					wit++
					witCells = append(witCells, addr)
				}
			}
			tot := 0
			for i := uint32(0); i < ramSize; i++ {
				if basePost[i] != post[i] {
					tot++
				}
			}
			if wit > 0 || tot > 100 {
				fmt.Printf("[%d/49] byte=%d bit=%d  total=%d witness=%d %v\n",
					step, byteIdx, bit, tot, wit, witCells)
			}
		}
	}
}
