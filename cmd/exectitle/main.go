// Command exectitle directly invokes the EXECUTE TITLE handler
// (softkey ID 0x98) via fcn.610 with d0 = 0x98. The Service Guide
// (Ch.3 p.205) documents EXECUTE TITLE as the softkey that runs the
// title buffer text AS an HP-IB command.
//
// Strategy:
//   1. Boot to operating loop;
//   2. Send the command via SendHPIB (PS/2-scancode encoded) so it
//      lands in the buffer at 0xFFBE02;
//   3. Set d0 = 0x98 and call fcn.610 (PC 0x610 -> jmp 0x0000E7A2)
//      via a synthesized BSR — by pushing a return address and
//      jumping into the handler;
//   4. Drive cycles, check IP-witness cells.
package main

import (
	"fmt"
	"os"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
	"github.com/windhooked/HP859X_SA/pkg/emu/machine"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

// Same ASCII → PS/2 scancode mapping as cmd/hpibascii.
var asciiToScancode = map[byte][]byte{
	'A': {0x1C}, 'B': {0x32}, 'C': {0x21}, 'D': {0x23}, 'E': {0x24},
	'F': {0x2B}, 'G': {0x34}, 'H': {0x33}, 'I': {0x43}, 'J': {0x3B},
	'K': {0x42}, 'L': {0x4B}, 'M': {0x3A}, 'N': {0x31}, 'O': {0x44},
	'P': {0x4D}, 'Q': {0x15}, 'R': {0x2D}, 'S': {0x1B}, 'T': {0x2C},
	'U': {0x3C}, 'V': {0x2A}, 'W': {0x1D}, 'X': {0x22}, 'Y': {0x35},
	'Z': {0x1A},
	'0': {0x45}, '1': {0x16}, '2': {0x1E}, '3': {0x26}, '4': {0x25},
	'5': {0x2E}, '6': {0x36}, '7': {0x3D}, '8': {0x3E}, '9': {0x46},
	';': {0x4C}, ' ': {0x29}, '.': {0x49}, ',': {0x41}, '-': {0x4E},
}

func encode(cmd string) ([]byte, error) {
	var out []byte
	for i, c := range []byte(cmd) {
		sc, ok := asciiToScancode[c]
		if !ok {
			return nil, fmt.Errorf("char %#02X at offset %d has no scancode mapping", c, i)
		}
		out = append(out, sc...)
	}
	return out, nil
}

// Cells fcn.520 (Initial Preset handler) explicitly clears — used as
// the success witness.
var ipClearCells = []uint32{
	0xFFAD6C, 0xFFAD6A, 0xFFAD74, 0xFFAD72, 0xFFAD6E, 0xFFAD70,
	0xFFA9AC, 0xFFB0EC, 0xFFB058, 0xFFAD64, 0xFFB20E, 0xFFBA5E,
	0xFFA5D4, 0xFFBF01, 0xFFBAF8,
}

func main() {
	cmd := "IP;"
	if len(os.Args) >= 2 {
		cmd = os.Args[1]
	}
	fmt.Printf("Direct EXECUTE TITLE handler invocation with command %q\n", cmd)
	fmt.Printf("(handler ID 0x98 via fcn.610 = fcn.E7A2)\n\n")

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

	scancodes, err := encode(cmd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "encode: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("encoded command as scancodes: %X\n\n", scancodes)

	// Baseline.
	mBase, _ := machine.New8593A(rom)
	mBase.CPU.Reset()
	mBase.BootToOperating(30_000_000)
	basePre := snapshot(mBase)
	mBase.Bus.Write(0xFFB1E0, bus.Word, 0x0200)
	mBase.Bus.Write(0xFFBEFA, bus.Word, 0x2000)
	mBase.CPU.SetReg(cpu.PC, 0x18ADC)
	for i := 0; i < 500_000; i++ {
		if err := mBase.CPU.Step(); err != nil {
			break
		}
	}
	basePost := snapshot(mBase)
	baseChanged := make(map[uint32]bool)
	for i := uint32(0); i < ramSize; i++ {
		if basePre[i] != basePost[i] {
			baseChanged[ramBase+i] = true
		}
	}

	// Run with command.
	m, _ := machine.New8593A(rom)
	m.CPU.Reset()
	m.BootToOperating(30_000_000)

	// Send scancodes via the normal IRQ4 path so they buffer at FFBE02.
	m.SendHPIB(scancodes, 5_000_000)

	// First drive the operating tick to actually run the parser
	// (popping bytes from bc12 into the ASCII buffer).
	m.Bus.Write(0xFFB1E0, bus.Word, 0x0200)
	m.Bus.Write(0xFFBEFA, bus.Word, 0x2000)
	m.CPU.SetReg(cpu.PC, 0x18ADC)
	for i := 0; i < 200_000; i++ {
		if err := m.CPU.Step(); err != nil {
			break
		}
	}

	// Verify buffer state.
	fmt.Printf("after parser tick: bc36=%#04X bc34=%#06X buffer at FFBE02 = ",
		m.Bus.Read(0xFFBC36, bus.Word),
		m.Bus.Read(0xFFBC34, bus.Long))
	for i := uint32(0); i < 16; i++ {
		b := byte(m.Bus.Read(0xFFBE02+i, bus.Byte))
		if b == 0 {
			break
		}
		if b >= 32 && b < 127 {
			fmt.Printf("%c", b)
		} else {
			fmt.Printf(".")
		}
	}
	fmt.Println()
	fmt.Println()

	// NOW directly invoke fcn.610 with d0 = 0x98 (EXECUTE TITLE handler ID).
	// Simulate a JSR to fcn.610: push a return address (a safe place to
	// land — we'll use a HALT NOP loop that we install). Easier: just
	// set PC to fcn.610 and run cycles, with d0 pre-loaded.
	fmt.Println("invoking fcn.610(d0=0x98) → EXECUTE TITLE handler")
	m.CPU.SetReg(cpu.D0, 0x98)
	m.CPU.SetReg(cpu.PC, 0x000610)

	// Run for many cycles to let the handler complete.
	for i := 0; i < 500_000; i++ {
		if err := m.CPU.Step(); err != nil {
			fmt.Printf("step %d error: %v at PC=%#06X\n", i, err, m.CPU.Reg(cpu.PC))
			break
		}
	}

	post := snapshot(m)

	// Witness check + summary.
	fmt.Println("\n=== IP-witness cells (cleared by fcn.520 / Initial Preset) ===")
	witnessChanged := 0
	for _, addr := range ipClearCells {
		off := addr - ramBase
		if basePre[off] != post[off] && !baseChanged[addr] {
			fmt.Printf("  %#06X  %#02X → %#02X  CHANGED\n",
				addr, basePre[off], post[off])
			witnessChanged++
		}
	}
	totalChanges := 0
	for i := uint32(0); i < ramSize; i++ {
		if basePre[i] != post[i] && !baseChanged[ramBase+i] {
			totalChanges++
		}
	}
	fmt.Printf("\n=== summary ===\n")
	fmt.Printf("  total command-specific cells changed : %d\n", totalChanges)
	fmt.Printf("  IP-witness cells changed             : %d of %d\n",
		witnessChanged, len(ipClearCells))
}
