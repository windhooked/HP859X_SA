// Command hpibkeyprobe sends a single PS/2 scancode and reports
// which witness cells changed — for systematically discovering which
// key triggers Initial Preset.
//
// Sends one scancode per run (the firmware's keyboard input path).
// Compares against a baseline run (no key sent). The IP-witness cells
// are addresses that fcn.520's IP handler body clears (so they would
// change FROM their post-boot values if IP fires).
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

// Cells that fcn.520 (IP handler at 0x4DF72) explicitly clears.
// If ANY of these changes from its post-boot value after a key, the
// key triggered (some part of) the IP path.
var ipClearCells = []uint32{
	0xFFAD6C, 0xFFAD6A, 0xFFAD74, 0xFFAD72, 0xFFAD6E, 0xFFAD70,
	0xFFA9AC, 0xFFB0EC, 0xFFB058, 0xFFAD64, 0xFFB20E, 0xFFBA5E,
	0xFFA5D4, 0xFFBF01, 0xFFBAF8,
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "usage: hpibkeyprobe <scancode-byte>\n")
		fmt.Fprintf(os.Stderr, "  example: hpibkeyprobe 0x76   (Esc)\n")
		os.Exit(1)
	}
	v, err := strconv.ParseUint(os.Args[1], 0, 8)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bad scancode: %v\n", err)
		os.Exit(1)
	}
	scancode := byte(v)
	fmt.Printf("Sending PS/2 scancode %#02X\n\n", scancode)

	rom, err := romloader.LoadDir("hp8593a_eeproms")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	const ramBase = uint32(0xFF0000)
	const ramSize = uint32(0x00F000)

	snapshot := func(m *machine.Machine) []byte {
		buf := make([]byte, ramSize)
		for i := uint32(0); i < ramSize; i++ {
			buf[i] = byte(m.Bus.Read(ramBase+i, bus.Byte))
		}
		return buf
	}

	runOne := func(send byte) (preRAM, postRAM []byte) {
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

	// Baseline: same boot + forced-tick steps, no key.
	basePre, basePost := runOne(0)
	baseChanged := make(map[uint32]bool)
	for i := uint32(0); i < ramSize; i++ {
		if basePre[i] != basePost[i] {
			baseChanged[ramBase+i] = true
		}
	}

	cmdPre, cmdPost := runOne(scancode)
	_ = cmdPre // pre is shared with base since boot is identical

	// Count BOTH IP-witness cells AND total command-specific changes.
	witnessChanged := 0
	for _, addr := range ipClearCells {
		off := addr - ramBase
		if basePre[off] != cmdPost[off] && !baseChanged[addr] {
			fmt.Printf("  %#06X  %#02X → %#02X  CHANGED (IP-witness)\n",
				addr, basePre[off], cmdPost[off])
			witnessChanged++
		}
	}
	totalChanges := 0
	for i := uint32(0); i < ramSize; i++ {
		if basePre[i] != cmdPost[i] && !baseChanged[ramBase+i] {
			totalChanges++
		}
	}
	fmt.Printf("\n=== summary ===\n")
	fmt.Printf("  baseline-filtered changes : %d cells total\n", totalChanges)
	fmt.Printf("  IP-witness cells changed  : %d of %d\n",
		witnessChanged, len(ipClearCells))

	// Dump all command-specific changes.
	fmt.Println("\n=== all command-specific cell changes (baseline-filtered) ===")
	for i := uint32(0); i < ramSize; i++ {
		addr := ramBase + i
		if basePre[i] != cmdPost[i] && !baseChanged[addr] {
			ch := '.'
			if cmdPost[i] >= 32 && cmdPost[i] < 127 {
				ch = rune(cmdPost[i])
			}
			fmt.Printf("  %#06X  %#02X → %#02X  |%c|\n",
				addr, basePre[i], cmdPost[i], ch)
		}
	}
}
