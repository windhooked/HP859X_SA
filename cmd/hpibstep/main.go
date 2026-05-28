// Command hpibstep is a focused PC-trace probe — single-steps the firmware
// after SendHPIB and records which key PCs it visits.
//
// Specifically, we ask the question: when an ASCII byte arrives in the bc12
// FIFO and the operating tick runs, does the firmware REACH fcn.567e0
// (the ASCII per-byte handler)? Or does it route to fcn.56d1a (the binary
// dispatcher), or skip entirely?
package main

import (
	"fmt"
	"os"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
	"github.com/windhooked/HP859X_SA/pkg/emu/machine"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

// Watch points: PCs along the input-parse pipeline. We record visit counts.
var watchPCs = map[uint32]string{
	0x58C2E: "fcn.58c2e entry (operating-tick HP-IB handler)",
	0x58C68: "fcn.58c2e LOOP TOP (pop next byte)",
	0x58C88: "fcn.58c2e call fcn.57278 (per-byte classifier)",
	0x58C90: "fcn.58c2e after-classify cmp 0xFFFF",
	0x58D4A: "fcn.58c2e normal-dispatch path (bit 13 clear)",
	0x58D50: "fcn.58c2e mask 0xFF00 (decide ASCII vs binary)",
	0x58D60: "fcn.58c2e ASCII branch (high byte 0)",
	0x58D68: "fcn.58c2e call fcn.567e0 (ASCII per-byte dispatcher)",
	0x58D56: "fcn.58c2e BINARY branch (high byte set)",
	0x58D5A: "fcn.58c2e call fcn.56d1a (binary dispatcher)",
	0x58D6C: "fcn.58c2e SKIP / next-byte",
	0x567E0: "fcn.567e0 entry (ASCII dispatcher)",
	0x564BE: "fcn.564be entry (ASCII handler — bc38==0 path)",
	0x564DA: "fcn.564da entry (ASCII handler — bc38!=0 path)",
	0x56414: "fcn.56414 entry (called from 564be when 9afb.7 SET)",
	0x563A6: "fcn.563a6 entry (called from 564be when 9afb.7 CLEAR — buffers byte)",
	0x57278: "fcn.57278 entry (per-byte classifier)",
	0x5714C: "fcn.5714c entry (keyboard scancode → ASCII table)",
}

func main() {
	cmd := "I"
	if len(os.Args) >= 2 {
		cmd = os.Args[1]
	}
	fmt.Printf("Stepping firmware after SendHPIB(%q) — watching parser PCs\n\n", cmd)

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
	m.BootToOperating(30_000_000)

	// Inject the ASCII byte(s).
	pending := m.SendHPIB([]byte(cmd), 5_000_000)
	if pending != 0 {
		fmt.Fprintf(os.Stderr, "WARN: %d bytes left at chip after send\n", pending)
	}
	bc28 := m.Bus.Read(0xFFBC28, bus.Word)
	bc26 := m.Bus.Read(0xFFBC26, bus.Word)
	fmt.Printf("after SendHPIB: bc12 has %d bytes pending (bc26=%#X bc28=%#X)\n",
		bc28-bc26, bc26, bc28)
	if bc28 == bc26 {
		fmt.Println("FATAL: no bytes pending in FIFO — nothing for parser to consume")
		os.Exit(1)
	}

	// Now step the CPU. We want to drive the operating tick. The current
	// DriveOperatingTick wrapper does Run(cycles) chunks; we replace that
	// with single-step so we can watch PC. This is slow, so limit to a few
	// hundred-thousand instructions.
	visits := make(map[uint32]int)
	const maxSteps = 200_000
	var firstSee uint32 = 0xFFFFFFFF

	// To make the operating tick run we mimic DriveOperatingTick by
	// pre-arming the RAM cells the tick body checks. The current behaviour
	// of m.DriveOperatingTick(0) would set those cells; instead we force
	// it manually then step.
	m.Bus.Write(0xFFB1E0, bus.Word, 0x0200) // tick body precondition
	m.Bus.Write(0xFFBEFA, bus.Word, 0x2000) // sweep-done bit so tick advances
	// DON'T touch 9afb — use whatever the boot left it at.
	m.CPU.SetReg(cpu.PC, 0x18ADC)            // force PC to the tick body
	fmt.Printf("9afb at trace start = %#02X\n", m.Bus.Read(0xFF9AFB, bus.Byte))
	fmt.Printf("bc36 at trace start = %#04X\n", m.Bus.Read(0xFFBC36, bus.Word))
	fmt.Printf("bc34 at trace start = %#06X\n", m.Bus.Read(0xFFBC34, bus.Long))

	for step := 0; step < maxSteps; step++ {
		pc := m.CPU.Reg(cpu.PC)
		if name, ok := watchPCs[pc]; ok {
			visits[pc]++
			if firstSee == 0xFFFFFFFF {
				firstSee = pc
				fmt.Printf("  first watch-PC at step %d: %#06X = %s\n", step, pc, name)
			}
		}
		if err := m.CPU.Step(); err != nil {
			fmt.Printf("step %d: CPU error: %v at PC=%#06X\n", step, err, pc)
			break
		}
	}

	fmt.Printf("\n=== PC visit summary (after %d steps) ===\n", maxSteps)
	for pc, name := range watchPCs {
		fmt.Printf("  %#06X  count=%d  %s\n", pc, visits[pc], name)
	}

	fmt.Printf("\n=== buffer state after step run ===\n")
	fmt.Printf("  bc26 = %#04X (parser FIFO read idx)\n", m.Bus.Read(0xFFBC26, bus.Word))
	fmt.Printf("  bc28 = %#04X (parser FIFO write idx)\n", m.Bus.Read(0xFFBC28, bus.Word))
	fmt.Printf("  bc32 = %#04X (ASCII buffer read idx?)\n", m.Bus.Read(0xFFBC32, bus.Word))
	fmt.Printf("  bc34 = %#06X (ASCII buffer base ptr?)\n", m.Bus.Read(0xFFBC34, bus.Long))
	fmt.Printf("  bc36 = %#04X (ASCII buffer write idx)\n", m.Bus.Read(0xFFBC36, bus.Word))
	fmt.Printf("  bc38 = %#04X (ASCII state)\n", m.Bus.Read(0xFFBC38, bus.Word))
	fmt.Printf("  bc2e = %#04X (count?)\n", m.Bus.Read(0xFFBC2E, bus.Word))
	fmt.Printf("  bc2c = %#04X (other ptr?)\n", m.Bus.Read(0xFFBC2C, bus.Word))
	// 0x563DE: writes byte to -0x41fe(a4) where a4 = mid-buffer pointer.
	// Likely the actual buffer is in the bdXX or beXX area. Let me look for any byte that's 'I' (0x49).
	fmt.Printf("\n=== scan for 'I' (0x49) anywhere in the bc12 FIFO buffer area ===\n")
	for addr := uint32(0xFFBC00); addr < 0xFFC100; addr += 16 {
		hexbytes := make([]byte, 16)
		for i := uint32(0); i < 16; i++ {
			hexbytes[i] = byte(m.Bus.Read(addr+i, bus.Byte))
		}
		// Print rows that contain anything non-zero
		nonZero := false
		for _, b := range hexbytes {
			if b != 0 {
				nonZero = true
				break
			}
		}
		if !nonZero {
			continue
		}
		fmt.Printf("  %06X ", addr)
		for _, b := range hexbytes {
			fmt.Printf("%02X ", b)
		}
		fmt.Printf("|")
		for _, b := range hexbytes {
			if b >= 32 && b < 127 {
				fmt.Printf("%c", b)
			} else {
				fmt.Printf(".")
			}
		}
		fmt.Println("|")
	}
}
