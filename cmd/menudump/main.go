// Command menudump reads the current menu's handler table from RAM
// at the location pointed to by FF9566 (the per-menu vtable pointer
// loaded into a0 by fcn.E7A2). Prints handler IDs vs the 113-entry
// softkey-label database so we can see which menu is currently
// active.
package main

import (
	"fmt"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/machine"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

// Reverse map of the 113-entry softkey label table from cmd/softkeys
// — extracted by walking the ROM at boot. Handler ID -> label text.
// (We could embed cmd/softkeys' extractor; for brevity, mirror its
// results here as a hardcoded small subset of the most interesting
// entries. cmd/softkeys is the canonical source.)
var labelByID = map[byte]string{
	0x00: "TV|Standard", 0x06: "More|1 of 3",
	0x16: "NORMLIZE|POSITION", 0x18: "More|2 of 3",
	0x1F: "HOLD",
	0x21: "Change|Title", 0x24: "More|1 of 2", 0x29: "PRINTER|SETUP",
	0x2A: "Previous|Menu",
	0x3D: "PEAK|SEARCH", 0x3E: "NEXT PK|RIGHT", 0x3F: "NEXT PK|LEFT",
	0x40: "CLEAR|OFFSET", 0x41: "CONTINUE", 0x42: "ABORT",
	0x4B: "DELETE|SEGMENT", 0x4C: "EDIT|DONE",
	0x52: "Time|Date", 0x53: "Change|Prefix",
	0x67: "RECALL|LIMIT", 0x68: "SAVE|LIMIT",
	0x73: "TRACE A", 0x74: "TRACE B", 0x75: "TRACE C",
	0x76: "LIMIT|LINES", 0x77: "AMP COR",
	0x80: "CAL|FREQ", 0x81: "CAL|AMPTD", 0x83: "CAL|STORE",
	0x84: "More|1 of 4", 0x85: "CONF|TEST", 0x86: "CAL|FETCH",
	0x88: "CRT VERT|POSITION", 0x89: "CRT HORZ|POSITION",
	0x8A: "More|2 of 4", 0x8B: "Service|Cal", 0x8C: "Service|Diag",
	0x8D: "DEFAULT|CAL DATA", 0x8E: "CAL|TRK GEN",
	0x97: "STOR PWR|ON UNITS", 0x98: "EXECUTE|TITLE",
	0x99: "Flatness|Data", 0x9A: "CAL|TIMEBASE",
	0xA7: "FM SPAN", 0xA9: "MAIN|SPAN",
}

func main() {
	rom, err := romloader.LoadDir("hp8593a_eeproms")
	if err != nil {
		panic(err)
	}
	m, _ := machine.New8593A(rom)
	m.CPU.Reset()
	m.BootToOperating(30_000_000)

	// FF9566 holds a long pointer to the active menu's handler table.
	// The pointer may be a sign-extended short-form 68K address — mask
	// to 24-bit bus.
	rawPtr := m.Bus.Read(0xFF9566, bus.Long)
	tableBase := rawPtr & 0xFFFFFF
	fmt.Printf("RAM[0xFF9566] = 0x%08X (raw) → 0x%06X (masked)\n\n",
		rawPtr, tableBase)

	if tableBase == 0 {
		fmt.Println("(no valid table loaded yet)")
		return
	}

	fmt.Println("=== handler table contents — raw bytes per 4-byte entry ===")
	for id := uint32(0); id < 0xA0; id++ {
		addr := tableBase + id*4
		if addr+4 > 0xFFFFFF {
			break
		}
		b0 := m.Bus.Read(addr+0, bus.Byte)
		b1 := m.Bus.Read(addr+1, bus.Byte)
		b2 := m.Bus.Read(addr+2, bus.Byte)
		b3 := m.Bus.Read(addr+3, bus.Byte)
		// If entries are <type><id_or_param><...> we want to see structure.
		lab := labelByID[byte(id)]
		if lab == "" {
			lab = "(unknown)"
		}
		fmt.Printf("  id=0x%02X  [%02X %02X %02X %02X]  %s\n",
			id, b0, b1, b2, b3, lab)
	}
}
