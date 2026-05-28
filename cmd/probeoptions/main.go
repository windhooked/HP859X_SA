// Command probeoptions reads the firmware's option-detection state
// after boot and prints a human-readable summary.
//
// The HP 8593A determines installed options via TWO RAM cells:
//
//	0x2FCB40 (CalRAM)  — stored model-ID code (1=85xx, 2=8595, 3=8596)
//	0xFFB00C            — board-strap reading (3 = 8593)
//
// At boot, fcn.1A3E0 (PC 0x1A410+) reads both and writes the resolved
// IDNUM value to:
//
//	0xFFBFEE — IDNUM (16-bit: 8590..8596 as 0x218E..0x2194)
//
// Downstream DLP and HP-IB code reads IDNUM directly and gates
// per-model behaviour off it (114+ DLP `HAVE()` checks and 48+
// `IF(IDNUM==NNNN)` branches in the firmware's DLP source).
//
// Option-installation flags (Option 026/027 freq extension, 041 IB,
// 043 RS-232, etc.) are stored in CalNVRAM and consumed by the same
// boot-time logic; the DLP `HAVE(BANDS)`, `HAVE(CNT)`, etc. queries
// route through to RAM cells set up from those flags.
package main

import (
	"fmt"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/machine"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

var idnumName = map[uint16]string{
	0x218E: "8590",
	0x218F: "8591",
	0x2190: "8592",
	0x2191: "8593",
	0x2192: "8594",
	0x2193: "8595",
	0x2194: "8596",
}

func main() {
	rom, err := romloader.LoadDir("hp8593a_eeproms")
	if err != nil {
		panic(err)
	}
	m, _ := machine.New8593A(rom)
	m.CPU.Reset()
	m.BootToOperating(30_000_000)

	idnum := uint16(m.Bus.Read(0xFFBFEE, bus.Word))
	calCode := uint16(m.Bus.Read(0x2FCB40, bus.Word))
	board := uint16(m.Bus.Read(0xFFB00C, bus.Word))

	model := idnumName[idnum]
	if model == "" {
		model = "(unknown)"
	}

	fmt.Println("HP 8593A firmware option-detection state after boot:")
	fmt.Println()
	fmt.Printf("  0xFFBFEE (IDNUM)        = %#06X  → model %s\n", idnum, model)
	fmt.Printf("  0x2FCB40 (CalRAM mdl)   = %#06X  (1=8595, 2=8594, 3=8596 if set)\n", calCode)
	fmt.Printf("  0xFFB00C (board strap)  = %#06X  (3 = 8593)\n", board)
	fmt.Println()
	fmt.Println("Boot resolution at fcn.1A3E0 / PC 0x1A410+:")
	fmt.Println("  if CalRAM[0x2FCB40] == 3 → IDNUM = 8596")
	fmt.Println("  elif CalRAM[0x2FCB40] == 2 → IDNUM = 8595")
	fmt.Println("  elif RAM[0xB00C] == 3      → IDNUM = 8593   ← our Rev L Opt-027")
	fmt.Println("  else                       → IDNUM = 8595")
	fmt.Println()
	fmt.Println("DLP/HP-IB code consumes IDNUM via the parser-table entry at")
	fmt.Println("ROM 0x07FD92 (`IDNUM ` with handler bytes 00 96 01 B6) and via")
	fmt.Println("114+ DLP `HAVE(BANDS|CNT|GATE|NBW|...)` option-presence checks")
	fmt.Println("that gate per-model + per-option behaviour throughout the ROM.")
}
