// Command bf05probe — empirical scan of bf05 (= the μC bridge status
// byte at MMIO 0xFFF160 mirrored to RAM 0xFFBF05) bit combinations,
// looking for whichever pattern triggers HP-IB command EXECUTION
// after a buffered IP;-style sequence.
//
// The IRQ4 handler at 0x2642 reads 0xFFF160 → 0xFFBF05 and dispatches
// based on bits 0,1,2,4,5 (identified). Bits 3, 6, 7 are unassigned in
// our trace and one likely encodes "EOI received / end-of-message" —
// signalling to the firmware that a complete HP-IB message has arrived
// and should be parsed + executed.
//
// This tool injects "IP;" as PS/2 scancodes via SendHPIB, then forces
// bf05 to each candidate bit pattern and runs the operating tick. It
// reports IP-witness-cell changes for each pattern.
package main

import (
	"fmt"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/machine"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

// PS/2 scancodes for "IP;"
var ipScancodes = []byte{0x43, 0x4D, 0x4C}

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

	// Baseline: identical boot + tick path, NO key sent and bf05 untouched.
	mB, _ := machine.New8593A(rom)
	mB.CPU.Reset()
	mB.BootToOperating(30_000_000)
	for i := 0; i < 5; i++ {
		mB.DriveOperatingTick(10_000_000)
	}
	basePost := snapshot(mB)
	witnessSet := make(map[uint32]bool, len(ipClearCells))
	for _, c := range ipClearCells {
		witnessSet[c] = true
	}

	// Focused sweep: just the patterns most likely to encode "end of
	// message / EOI / command-complete" signals. The IRQ4 handler tests
	// bits 0, 1, 2, 4, 5 — bits 3, 6, 7 are unaccounted for and one
	// likely carries the EOI/end-of-message indicator.
	candidates := []uint32{
		0x00, // baseline (no μC signal)
		0x03, // bits 0+1 — what SendHPIB sets (byte ready)
		0x07, // 0x03 + bit 2 (general dispatcher signal)
		0x0B, // 0x03 + bit 3 (unknown ← candidate for EOI)
		0x13, // 0x03 + bit 4 (front-panel key signal)
		0x23, // 0x03 + bit 5 (clear f150 / fcn.22f0)
		0x43, // 0x03 + bit 6 (unknown)
		0x83, // 0x03 + bit 7 (unknown)
		0xCB, // bits 0+1+3+6+7 — multi-flag end-of-message
		0xFF, // all bits — "everything ready"
	}
	fmt.Printf("scanning %d focused bf05 patterns (with IP; pre-buffered)...\n", len(candidates))
	fmt.Println("(printing ALL patterns + IP-witness count)")
	any := false
	for _, pattern := range candidates {
		m, _ := machine.New8593A(rom)
		m.CPU.Reset()
		m.BootToOperating(30_000_000)

		// Send the buffered IP; scancodes.
		m.SendHPIB(ipScancodes, 5_000_000)

		// Force bf05 to the candidate pattern (model what the μC would put
		// in f160 → bf05 for "end of message").
		m.Bus.Write(0xFFBF05, bus.Byte, pattern)

		// Drive ticks.
		for i := 0; i < 5; i++ {
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
		marker := ""
		if wit > 0 {
			any = true
			marker = " *** WITNESS HIT ***"
		}
		fmt.Printf("  bf05=%#02X  witness=%d/%d  total=%d cells%s\n",
			pattern, wit, len(ipClearCells), tot, marker)
	}
	if !any {
		fmt.Println("\n(no bf05 pattern fires IP-witness cells)")
	}
}
