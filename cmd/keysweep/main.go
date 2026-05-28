// Command keysweep boots ONCE then for each PS/2 scancode 0x01..0xFF
// forks a fresh machine state (re-snapshots from the boot), sends the
// scancode, drives the operating tick, and reports baseline-filtered
// changes including IP-witness cells (cells fcn.520 clears).
//
// Single-binary internalisation of the sweep — avoids per-scancode
// `go run` overhead, runs in seconds rather than minutes.
package main

import (
	"fmt"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
	"github.com/windhooked/HP859X_SA/pkg/emu/machine"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

// Cells fcn.520 explicitly clears (Initial Preset side effects).
// Any non-baseline change to one of these signals IP fired (or
// some related state-reset path).
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

func runOne(rom []byte, send byte) (preRAM, postRAM []byte) {
	m, _ := machine.New8593A(rom)
	m.CPU.Reset()
	m.BootToOperating(30_000_000)
	preRAM = snapshot(m)
	if send != 0 {
		m.SendHPIB([]byte{send}, 5_000_000)
	}
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

	fmt.Println("[1/258] running baseline (no key)...")
	basePre, basePost := runOne(rom, 0)
	baseChanged := make(map[uint32]bool)
	for i := uint32(0); i < ramSize; i++ {
		if basePre[i] != basePost[i] {
			baseChanged[ramBase+i] = true
		}
	}
	fmt.Printf("baseline: %d cells change during forced tick\n\n", len(baseChanged))

	witnessSet := make(map[uint32]bool, len(ipClearCells))
	for _, a := range ipClearCells {
		witnessSet[a] = true
	}

	type result struct {
		scancode byte
		total    int
		witness  int
		witCells []uint32
	}
	var results []result

	for sc := 1; sc <= 0xFF; sc++ {
		if sc%32 == 0 {
			fmt.Printf("[%d/258] swept through 0x%02X...\n", sc+1, sc)
		}
		_, post := runOne(rom, byte(sc))
		var r result
		r.scancode = byte(sc)
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

	// Sort and report the most interesting hits.
	fmt.Println("\n=== ALL scancodes that fire any IP-witness cell ===")
	any := false
	for _, r := range results {
		if r.witness > 0 {
			any = true
			fmt.Printf("  scancode %#02X  witness=%d/%d  total=%d cells  witCells=%v\n",
				r.scancode, r.witness, len(ipClearCells), r.total, r.witCells)
		}
	}
	if !any {
		fmt.Println("  (none — no single scancode fires Initial Preset)")
	}

	fmt.Println("\n=== top 15 scancodes by total RAM impact ===")
	// simple top-N
	type kv struct {
		sc    byte
		total int
	}
	var byTotal []kv
	for _, r := range results {
		byTotal = append(byTotal, kv{r.scancode, r.total})
	}
	for i := 0; i < len(byTotal); i++ {
		for j := i + 1; j < len(byTotal); j++ {
			if byTotal[j].total > byTotal[i].total {
				byTotal[i], byTotal[j] = byTotal[j], byTotal[i]
			}
		}
	}
	for i := 0; i < 15 && i < len(byTotal); i++ {
		fmt.Printf("  %#02X  → %d cells\n", byTotal[i].sc, byTotal[i].total)
	}
}
