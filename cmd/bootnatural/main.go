// Command bootnatural boots the 8593A with NO LoopBreaker — only the legitimate
// IRQ5 timer tick — to see whether the boot-time compute loops (ROM checksum,
// march RAM test, calibration delay) pass naturally with real RAM + correct ROM.
// It samples the PC over time so a genuine stall (vs. a slow-but-progressing
// loop) is visible. This guides the "initialise hardware to boot state" work:
// loops that pass naturally don't need a LoopBreaker hack.
//
// Usage:
//
//	go run ./cmd/bootnatural/ [cycles]   # default 120 000 000
package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
	"github.com/windhooked/HP859X_SA/pkg/emu/machine"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

// regionName labels the known boot-time loops so a stall is identifiable.
func regionName(pc uint32) string {
	switch {
	case pc >= 0x1F5A && pc <= 0x1F7E:
		return "ROM-checksum"
	case pc >= 0x2160 && pc <= 0x21F6:
		return "march-RAM-test"
	case pc >= 0x2420 && pc <= 0x2428:
		return "cal-delay"
	case pc >= 0x5100 && pc <= 0x51C0:
		return "MAIN-LOOP"
	case pc >= 0x10300 && pc <= 0x10400:
		return "sweep-service"
	default:
		return ""
	}
}

func main() {
	total := 120_000_000
	if len(os.Args) > 1 {
		if n, err := strconv.Atoi(os.Args[1]); err == nil && n > 0 {
			total = n
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

	const chunk = 2000
	const irqPeriod = 5
	const irqService = 400
	const sampleEvery = 1_000_000 // print a PC sample every 1M cycles

	reachedOperating := false
	nextSample := sampleEvery

	for done := 0; done < total; done += chunk {
		m.CPU.Run(chunk)
		pc := m.CPU.Reg(cpu.PC)

		if (done/chunk)%irqPeriod == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Run(irqService)
			m.CPU.SetIRQ(0)
		}
		if pc >= 0x5000 && pc < 0x12000 {
			reachedOperating = true
		}
		if done >= nextSample {
			fmt.Printf("  @%3dM cycles: PC=%06X %s\n", done/1_000_000, pc, regionName(pc))
			nextSample += sampleEvery
		}
	}

	fmt.Printf("=== final PC=%06X  reachedOperating=%v ===\n",
		m.CPU.Reg(cpu.PC), reachedOperating)
	fmt.Printf("display: moves=%d glyphs=%d\n", m.MMIO.Display.Moves, m.MMIO.Display.Glyphs)
}
