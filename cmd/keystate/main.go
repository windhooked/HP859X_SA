// Command keystate boots the HP 8593A to its operating loop and samples
// the Rev L key-dispatch state machine — RAM[0xFFBF03] (event flag),
// RAM[0xFFBF0A] (pending-function pointer), RAM[0xFFBC67] (IRQ3-set
// key-available flag), and a few related state bytes. Goal: determine
// whether the firmware ever reaches a state in which fcn.1B40 can
// dispatch to the key consumer at 0x148 → 0x18568 (which requires bf03==0
// AND bf0a==0 at the test point).
//
// Usage:
//
//	go run ./cmd/keystate/ [cycles]   # default 30_000_000
//
// The boot uses the canonical Machine.BootToOperating; after boot the
// machine is single-stepped while sampling the RAM bytes once per chunk,
// printing a transition log.
package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
	"github.com/windhooked/HP859X_SA/pkg/emu/machine"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

func main() {
	cycles := 30_000_000
	if len(os.Args) > 1 {
		if n, err := strconv.Atoi(os.Args[1]); err == nil && n > 0 {
			cycles = n
		}
	}

	rom, err := romloader.LoadDir("hp8593a_eeproms")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	m, err := machine.New8593A(rom)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	m.CPU.Reset()
	m.BootToOperating(cycles)

	read := func(addr uint32, sz bus.Size) uint32 { return m.Bus.Read(addr, sz) }

	// Sample once after boot, then drive a few sample windows.
	dump := func(label string) {
		bf03 := read(0xFFBF03, bus.Byte)
		bf0a := read(0xFFBF0A, bus.Long)
		bc67 := read(0xFFBC67, bus.Byte)
		befb := read(0xFFBEFB, bus.Byte)
		bf12 := read(0xFFBF12, bus.Long)
		bf16 := read(0xFFBF16, bus.Long)
		fmt.Printf("%-14s PC=%06X  bf03=%02X  bf0a=%08X  bc67=%02X  befb=%02X  bf12=%08X  bf16=%08X\n",
			label, m.CPU.Reg(cpu.PC), bf03, bf0a, bc67, befb, bf12, bf16)
	}

	fmt.Println("After BootToOperating:")
	dump("post-boot")

	// Step the machine forward, injecting IRQ5 between chunks. The Rev L
	// IRQ5 handler (ROM 0x3ECE) increments bf12 — the timer counter the
	// operating loop's busy-poll at PC 0x7C7E/0x4824 is waiting on. Without
	// these ticks bf12 stays at its boot value and the poll never returns.
	const chunkCycles = 50_000
	for i := 0; i < 80; i++ {
		m.CPU.Run(chunkCycles)
		m.CPU.SetIRQ(5)
		m.CPU.Run(400)
		m.CPU.SetIRQ(0)
		if i%4 == 0 {
			dump(fmt.Sprintf("t+%2dx50k", i+1))
		}
	}

	// Now inject a key (any matrix bit) and watch.
	fmt.Println("\nInjecting IRQ3 + key matrix (bit set at row0/col0):")
	var matrix [6]byte
	matrix[0] = 0x01
	m.FrontPanel.InjectMatrix(matrix)
	m.CPU.SetIRQ(3)
	m.CPU.Run(400)
	m.CPU.SetIRQ(0)
	dump("post-IRQ3")

	for i := 0; i < 80; i++ {
		m.CPU.Run(chunkCycles)
		m.CPU.SetIRQ(5)
		m.CPU.Run(400)
		m.CPU.SetIRQ(0)
		if i%4 == 0 {
			dump(fmt.Sprintf("k+%2dx50k", i+1))
		}
	}
}
