// Command keymatrix sweeps all 48 front-panel key-matrix bit positions
// looking for the one that fires Initial Preset. For each (byte, bit)
// position 0..5 × 0..7, the tool boots a fresh machine, calls
// FrontPanel.SetBit(byte, bit), drives IRQ3 + forced operating tick,
// and reports baseline-filtered RAM-cell changes including the
// IP-witness cells (those fcn.520 clears).
//
// This is the front-panel analog of cmd/keysweep — that tool injects
// PS/2 scancodes through the IRQ4 path (external HP-IB/keyboard);
// this tool injects matrix bits through the IRQ3 path (the dedicated
// front-panel μC).
package main

import (
	"fmt"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
	"github.com/windhooked/HP859X_SA/pkg/emu/machine"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

// Cells fcn.520 explicitly clears (Initial Preset side effects).
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

// runOne boots, optionally presses a matrix bit, then drives the
// operating tick. byteIdx/bit < 0 → baseline (no key pressed).
func runOne(rom []byte, byteIdx, bit int) (preRAM, postRAM []byte) {
	m, _ := machine.New8593A(rom)
	m.CPU.Reset()
	m.BootToOperating(30_000_000)
	preRAM = snapshot(m)

	if byteIdx >= 0 {
		m.FrontPanel.SetBit(byteIdx, bit)
		// IRQ3 — handler sets bc67 bit 0.
		m.CPU.SetIRQ(3)
		m.CPU.Run(400)
		m.CPU.SetIRQ(0)
	}

	// Drive operating tick: pre-arm + force PC + step.
	m.Bus.Write(0xFFB1E0, bus.Word, 0x0200)
	m.Bus.Write(0xFFBEFA, bus.Word, 0x2000)
	m.CPU.SetReg(cpu.PC, 0x18ADC)
	for i := 0; i < 500_000; i++ {
		if err := m.CPU.Step(); err != nil {
			break
		}
	}
	postRAM = snapshot(m)
	return
}

func main() {
	rom, err := romloader.LoadDir("hp8593a_eeproms")
	if err != nil {
		panic(err)
	}

	fmt.Println("[1/49] baseline (no key pressed)...")
	basePre, basePost := runOne(rom, -1, -1)
	baseChanged := make(map[uint32]bool)
	for i := uint32(0); i < ramSize; i++ {
		if basePre[i] != basePost[i] {
			baseChanged[ramBase+i] = true
		}
	}
	fmt.Printf("  baseline: %d cells change ambient\n\n", len(baseChanged))

	witnessSet := make(map[uint32]bool, len(ipClearCells))
	for _, a := range ipClearCells {
		witnessSet[a] = true
	}

	type result struct {
		byteIdx, bit int
		total        int
		witness      int
		witCells     []uint32
	}
	var results []result

	step := 1
	for byteIdx := 0; byteIdx < 6; byteIdx++ {
		for bit := 0; bit < 8; bit++ {
			step++
			fmt.Printf("[%d/49] byte=%d bit=%d ...\n", step, byteIdx, bit)
			_, post := runOne(rom, byteIdx, bit)
			var r result
			r.byteIdx = byteIdx
			r.bit = bit
			for i := uint32(0); i < ramSize; i++ {
				if basePre[i] != post[i] && !baseChanged[ramBase+i] {
					r.total++
					if witnessSet[ramBase+i] {
						r.witness++
						r.witCells = append(r.witCells, ramBase+i)
					}
				}
			}
			results = append(results, r)
		}
	}

	fmt.Println("\n=== ALL matrix bits that fire any IP-witness cell ===")
	any := false
	for _, r := range results {
		if r.witness > 0 {
			any = true
			fmt.Printf("  byte=%d bit=%d  witness=%d/%d  total=%d cells  cells=%v\n",
				r.byteIdx, r.bit, r.witness, len(ipClearCells), r.total, r.witCells)
		}
	}
	if !any {
		fmt.Println("  (none — no single bit fires Initial Preset)")
	}

	fmt.Println("\n=== top 10 matrix bits by total RAM impact ===")
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].total > results[i].total {
				results[i], results[j] = results[j], results[i]
			}
		}
	}
	for i := 0; i < 10 && i < len(results); i++ {
		r := results[i]
		fmt.Printf("  byte=%d bit=%d  → %d cells\n", r.byteIdx, r.bit, r.total)
	}
}
