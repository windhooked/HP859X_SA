// Command hpibtrace sends a single HP-IB command via SendHPIB +
// DriveOperatingTick and reports which RAM addresses changed value
// — empirical hunt for per-command handler RAM cells (e.g. CF stores
// center frequency in some specific location).
//
// Usage:
//
//	go run ./cmd/hpibtrace/ "CF1GZ;"
//
// Output: addresses where the post-parser RAM differs from the pre-
// parser RAM, plus their before/after values.
package main

import (
	"fmt"
	"os"
	"sort"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/machine"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "usage: hpibtrace <command-string>\n")
		fmt.Fprintf(os.Stderr, "  example: hpibtrace 'CF1GZ;'\n")
		os.Exit(1)
	}
	cmd := os.Args[1]
	fmt.Printf("Tracing HP-IB command: %q\n\n", cmd)

	rom, err := romloader.LoadDir("hp8593a_eeproms")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	const ramBase = uint32(0xFF0000)
	const ramSize = uint32(0x00F000)

	// Snapshot helper.
	snapshot := func(m *machine.Machine) []byte {
		buf := make([]byte, ramSize)
		for i := uint32(0); i < ramSize; i++ {
			buf[i] = byte(m.Bus.Read(ramBase+i, bus.Byte))
		}
		return buf
	}

	// Baseline run: boot + DriveOperatingTick WITHOUT a command.
	// This captures all the "ambient" writes that happen during the
	// tick (stack churn, IRQ5 counters, display state, etc).
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

	// Command run: boot + Send + DriveOperatingTick.
	fmt.Printf("Command run: %q\n", cmd)
	m, err := machine.New8593A(rom)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	m.CPU.Reset()
	m.BootToOperating(30_000_000)
	before := snapshot(m)
	pending := m.SendHPIB([]byte(cmd), 5_000_000)
	if pending != 0 {
		fmt.Fprintf(os.Stderr, "WARN: %d bytes left at chip after send\n", pending)
	}
	endPC := m.DriveOperatingTick(20_000_000)
	after := snapshot(m)

	fmt.Printf("after DriveOperatingTick: end PC=%#06X\n", endPC)
	fmt.Printf("  bc26 = %#04X (parser read idx)\n", m.Bus.Read(0xFFBC26, bus.Word))
	fmt.Printf("  bc28 = %#04X (parser write idx)\n", m.Bus.Read(0xFFBC28, bus.Word))
	// ASCII-buffer state cells used by fcn.567e0 / fcn.56dae:
	// bc32 = read idx, bc34 = base ptr (longword), bc36 = write idx, bc38 = state flag.
	// Print these BEFORE filtering — they tell us if the ASCII parser ran.
	fmt.Printf("  bc32 = %#04X bc34 = %#06X (long) bc36 = %#04X bc38 = %#04X\n",
		m.Bus.Read(0xFFBC32, bus.Word),
		m.Bus.Read(0xFFBC34, bus.Long),
		m.Bus.Read(0xFFBC36, bus.Word),
		m.Bus.Read(0xFFBC38, bus.Word))
	// Compare to baseline values for these cells:
	fmt.Printf("  (baseline at same cells: bc32=%#04X bc34=%#06X bc36=%#04X bc38=%#04X)\n",
		mBase.Bus.Read(0xFFBC32, bus.Word),
		mBase.Bus.Read(0xFFBC34, bus.Long),
		mBase.Bus.Read(0xFFBC36, bus.Word),
		mBase.Bus.Read(0xFFBC38, bus.Word))
	// Parser state bits used by fcn.58C2E / fcn.57278 / fcn.5714c:
	fmt.Printf("  parser state: bc64=%#06X bc65=%#04X (mode bits) 9afb=%#04X (bit 7 = handler-select)\n\n",
		m.Bus.Read(0xFFBC64, bus.Word),
		m.Bus.Read(0xFFBC65, bus.Byte),
		m.Bus.Read(0xFF9AFB, bus.Byte))

	// Diff: find addresses that CHANGED.
	type change struct {
		addr     uint32
		oldByte  byte
		newByte  byte
	}
	var changes []change
	for i := uint32(0); i < ramSize; i++ {
		if before[i] != after[i] {
			addr := ramBase + i
			// Filter out addresses that ALSO changed in the baseline
			// run — those are ambient tick noise, not command effects.
			if baseChanged[addr] {
				continue
			}
			changes = append(changes, change{
				addr:    addr,
				oldByte: before[i],
				newByte: after[i],
			})
		}
	}

	fmt.Printf("%d COMMAND-SPECIFIC byte changes (after filtering %d baseline changes).\n\n",
		len(changes), len(baseChanged))

	// Group adjacent changes into runs of up to 4 bytes (so we can
	// see word/long writes naturally).
	if len(changes) > 200 {
		fmt.Printf("(too many; only showing first 200)\n\n")
		changes = changes[:200]
	}

	// Sort by address.
	sort.Slice(changes, func(i, j int) bool { return changes[i].addr < changes[j].addr })

	// Print in grouped form.
	i := 0
	for i < len(changes) {
		runStart := i
		for i < len(changes)-1 && changes[i+1].addr == changes[i].addr+1 && i-runStart < 7 {
			i++
		}
		// Print this run.
		fmt.Printf("  [%08X..%08X]  old: ", changes[runStart].addr, changes[i].addr)
		for j := runStart; j <= i; j++ {
			fmt.Printf("%02X ", changes[j].oldByte)
		}
		fmt.Printf(" → new: ")
		for j := runStart; j <= i; j++ {
			fmt.Printf("%02X ", changes[j].newByte)
		}
		// Decode as readable for ASCII-ish.
		fmt.Printf(" |")
		for j := runStart; j <= i; j++ {
			b := changes[j].newByte
			if b >= 32 && b < 127 {
				fmt.Printf("%c", b)
			} else {
				fmt.Printf(".")
			}
		}
		fmt.Printf("|")
		fmt.Println()
		i++
	}
}
