// Command hpibascii sends an ASCII command string by reverse-translating
// each character to its PS/2 Set-2 scancode, then injecting the scancode
// sequence through SendHPIB (which the firmware interprets as keyboard
// input from the front-panel μC bridge).
//
// This models the REAL 8593A architecture: HP-IB bytes arrive at the
// TMS9914A chip, the front-panel μC reads them and re-emits them to the
// CPU as PS/2 scancodes (since the CPU's input stack only knows the PS/2
// protocol). The CPU's translation table at ROM 0x55C28 then maps each
// scancode back to ASCII for buffering.
//
// Usage:
//   go run ./cmd/hpibascii/ "CF1GZ;"
package main

import (
	"fmt"
	"os"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/machine"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

// asciiToScancode maps ASCII chars to PS/2 Set-2 scancodes (unshifted
// for letters; the firmware's case-fold mode auto-uppercases lowercase
// hits, so for letters we send the unshifted scancode regardless of
// case). For chars that require shift (the punctuation we use:
// `;` `:`), we'd need to send shift-down (0x12) + scancode + shift-up
// (0xF0 0x12) — but `;` is unshifted, so for typical SCPI-ish commands
// we get away without shift sequences.
//
// Derived empirically from the firmware's ROM 0x55C28 table.
var asciiToScancode = map[byte][]byte{
	// Letters (unshifted; case-fold mode upgrades to uppercase)
	'A': {0x1C}, 'B': {0x32}, 'C': {0x21}, 'D': {0x23}, 'E': {0x24},
	'F': {0x2B}, 'G': {0x34}, 'H': {0x33}, 'I': {0x43}, 'J': {0x3B},
	'K': {0x42}, 'L': {0x4B}, 'M': {0x3A}, 'N': {0x31}, 'O': {0x44},
	'P': {0x4D}, 'Q': {0x15}, 'R': {0x2D}, 'S': {0x1B}, 'T': {0x2C},
	'U': {0x3C}, 'V': {0x2A}, 'W': {0x1D}, 'X': {0x22}, 'Y': {0x35},
	'Z': {0x1A},
	'a': {0x1C}, 'b': {0x32}, 'c': {0x21}, 'd': {0x23}, 'e': {0x24},
	'f': {0x2B}, 'g': {0x34}, 'h': {0x33}, 'i': {0x43}, 'j': {0x3B},
	'k': {0x42}, 'l': {0x4B}, 'm': {0x3A}, 'n': {0x31}, 'o': {0x44},
	'p': {0x4D}, 'q': {0x15}, 'r': {0x2D}, 's': {0x1B}, 't': {0x2C},
	'u': {0x3C}, 'v': {0x2A}, 'w': {0x1D}, 'x': {0x22}, 'y': {0x35},
	'z': {0x1A},
	// Digits (unshifted)
	'0': {0x45}, '1': {0x16}, '2': {0x1E}, '3': {0x26}, '4': {0x25},
	'5': {0x2E}, '6': {0x36}, '7': {0x3D}, '8': {0x3E}, '9': {0x46},
	// Punctuation (unshifted only; will not emit shift sequences)
	';': {0x4C}, ' ': {0x29}, '.': {0x49}, ',': {0x41}, '-': {0x4E},
	'/': {0x4A}, '=': {0x55}, '[': {0x54}, '\\': {0x5D}, ']': {0x5B},
	'\'': {0x52}, '`': {0x0E},
	'\n': {0x5A}, // Enter
	'\b': {0x66}, // Backspace
	'\t': {0x0D}, // Tab
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

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "usage: hpibascii <ASCII-command>\n")
		fmt.Fprintf(os.Stderr, "  example: hpibascii 'CF1GZ;'\n")
		os.Exit(1)
	}
	cmd := os.Args[1]
	fmt.Printf("Sending ASCII command %q (encoded as PS/2 scancodes)\n", cmd)

	scancodes, err := encode(cmd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "encode: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  scancode sequence: %X (%d bytes)\n\n", scancodes, len(scancodes))

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

	// Baseline: boot + DriveOperatingTick without sending anything.
	fmt.Println("Baseline run (no command)...")
	mBase, _ := machine.New8593A(rom)
	mBase.CPU.Reset()
	mBase.BootToOperating(30_000_000)
	baseBefore := snapshot(mBase)
	_ = mBase.DriveOperatingTick(20_000_000)
	baseAfter := snapshot(mBase)
	baseChanged := make(map[uint32]bool)
	for i := uint32(0); i < ramSize; i++ {
		if baseBefore[i] != baseAfter[i] {
			baseChanged[ramBase+i] = true
		}
	}
	fmt.Printf("  baseline: %d bytes changed (filtering these out)\n\n", len(baseChanged))

	// Command run.
	fmt.Printf("Command run: %q (%d scancodes)\n", cmd, len(scancodes))
	m, err := machine.New8593A(rom)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	m.CPU.Reset()
	m.BootToOperating(30_000_000)
	before := snapshot(m)

	pending := m.SendHPIB(scancodes, 10_000_000)
	if pending != 0 {
		fmt.Fprintf(os.Stderr, "WARN: %d bytes left at chip after send\n", pending)
	}
	endPC := m.DriveOperatingTick(20_000_000)
	after := snapshot(m)

	fmt.Printf("after DriveOperatingTick: end PC=%#06X\n", endPC)
	fmt.Printf("  bc26=%#04X bc28=%#04X (parser FIFO)\n",
		m.Bus.Read(0xFFBC26, bus.Word), m.Bus.Read(0xFFBC28, bus.Word))
	fmt.Printf("  bc36=%#04X (ASCII buffer write idx) bc34=%#06X\n",
		m.Bus.Read(0xFFBC36, bus.Word), m.Bus.Read(0xFFBC34, bus.Long))
	// Print contents of the ASCII buffer.
	fmt.Printf("  ASCII buffer @ 0xFFBE02..0xFFBE40:\n    ")
	for i := uint32(0); i < 0x40; i++ {
		b := byte(m.Bus.Read(0xFFBE02+i, bus.Byte))
		if b >= 32 && b < 127 {
			fmt.Printf("%c", b)
		} else if b == 0 {
			break
		} else {
			fmt.Printf(".")
		}
	}
	fmt.Println()
	fmt.Println()

	// Diff command-specific changes (filter baseline).
	type change struct {
		addr             uint32
		oldByte, newByte byte
	}
	var changes []change
	for i := uint32(0); i < ramSize; i++ {
		if before[i] != after[i] {
			addr := ramBase + i
			if baseChanged[addr] {
				continue
			}
			changes = append(changes, change{addr, before[i], after[i]})
		}
	}
	fmt.Printf("%d COMMAND-SPECIFIC byte changes (after filtering %d baseline).\n\n",
		len(changes), len(baseChanged))

	if len(changes) > 80 {
		fmt.Printf("(showing first 80)\n")
		changes = changes[:80]
	}
	for _, c := range changes {
		ch := '.'
		if c.newByte >= 32 && c.newByte < 127 {
			ch = rune(c.newByte)
		}
		fmt.Printf("  [%08X]  %02X → %02X  |%c|\n", c.addr, c.oldByte, c.newByte, ch)
	}
}
