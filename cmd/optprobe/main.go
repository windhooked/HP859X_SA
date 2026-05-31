// Command optprobe empirically locates the SOURCE of "which options are
// installed" that the firmware uses to build the power-up banner
// ("004: OVEN", "021: HPIB", "026: FRQEXT", ...).
//
// It boots the canonical Rev L image and uses the bus OnRead/OnWrite hooks to
// capture, with PC context:
//
//   - every read of the A16 system-ID hardware-strap MMIO words
//     (0xFFF73C / 0xFFF73E / 0xFFF77C / 0xFFF77E) and the value returned;
//   - the write that latches them into the RAM "installed-options" longwords
//     at 0xFFBF26 / 0xFFBF2A;
//   - the per-option presence reads during banner build of the config-shadow
//     RAM cells (0xFFBF09 HPIB/RS232 selector, 0xFFBFEE model/IDNUM,
//     0xFFAD7C A7-analog-bus latch used by OVEN, 0xFFBF26 options mask).
//
// This is a read-only investigation tool; it changes NO emulator behaviour.
package main

import (
	"fmt"
	"os"
	"sort"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
	"github.com/windhooked/HP859X_SA/pkg/emu/machine"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

func main() {
	rom, err := romloader.LoadDir("hp8593a_eeproms")
	if err != nil {
		fmt.Println("load rom:", err)
		os.Exit(1)
	}
	m, err := machine.New8593A(rom)
	if err != nil {
		fmt.Println("new machine:", err)
		os.Exit(1)
	}
	m.CPU.Reset()

	cycles := 30_000_000
	if len(os.Args) > 1 {
		fmt.Sscanf(os.Args[1], "%d", &cycles)
	}

	// Watched addresses → label.
	watchMMIO := map[uint32]string{
		0xFFF73C: "STRAP_73C", 0xFFF73E: "STRAP_73E",
		0xFFF77C: "STRAP_77C", 0xFFF77E: "STRAP_77E",
		0xFFF110: "MMIO_F110(HPIB/RS232 src)",
	}
	// RAM config-shadow cells read by the banner presence tests.
	watchRAM := map[uint32]string{
		0xFFBF26: "OPTMASK_A.l", 0xFFBF2A: "OPTMASK_B.l",
		0xFFBF09: "HPIB/RS232 sel", 0xFFBFEE: "IDNUM/model",
		0xFFAD7C: "A7bus latch (OVEN)", 0xFFAD4C: "TG", 0xFFB00C: "board_id",
	}

	type ev struct {
		pc, addr, val uint32
		sz            bus.Size
		w             bool
		label         string
	}
	var log []ev
	strapReads := map[uint32]uint32{}
	maskWrites := map[uint32][]ev{}

	rec := func(addr uint32, sz bus.Size, val uint32, w bool) {
		pc := m.CPU.Reg(cpu.PC)
		if lbl, ok := watchMMIO[addr]; ok {
			log = append(log, ev{pc, addr, val, sz, w, lbl})
			if !w {
				strapReads[addr] = val
			}
		}
		if lbl, ok := watchRAM[addr]; ok {
			e := ev{pc, addr, val, sz, w, lbl}
			log = append(log, e)
			if w && (addr == 0xFFBF26 || addr == 0xFFBF2A) {
				maskWrites[addr] = append(maskWrites[addr], e)
			}
		}
	}

	m.Bus.OnRead = func(a uint32, s bus.Size, v uint32) { rec(a, s, v, false) }
	m.Bus.OnWrite = func(a uint32, s bus.Size, v uint32) { rec(a, s, v, true) }

	m.BootToOperating(cycles)

	fmt.Printf("== optprobe: booted %d cycles ==\n\n", cycles)

	fmt.Println("--- A16 system-ID hardware-strap MMIO reads (the PCB jumper register) ---")
	addrs := []uint32{0xFFF73C, 0xFFF73E, 0xFFF77C, 0xFFF77E}
	for _, a := range addrs {
		if v, ok := strapReads[a]; ok {
			fmt.Printf("  read 0x%06X = 0x%04X  (%s)\n", a, v, watchMMIO[a])
		} else {
			fmt.Printf("  read 0x%06X = (never read)  (%s)\n", a, watchMMIO[a])
		}
	}

	fmt.Println("\n--- writes that build the RAM installed-options longwords ---")
	for _, a := range []uint32{0xFFBF26, 0xFFBF2A} {
		for _, e := range maskWrites[a] {
			fmt.Printf("  PC 0x%05X  WRITE 0x%06X = 0x%X  sz=%d  (%s)\n",
				e.pc, e.addr, e.val, e.sz, e.label)
		}
	}

	// Show the first read of each watched cell by each distinct PC (the
	// per-option presence test sites), capped.
	fmt.Println("\n--- distinct (PC, addr) read sites for option presence cells ---")
	seen := map[[2]uint32]ev{}
	for _, e := range log {
		if e.w {
			continue
		}
		if _, isRAM := watchRAM[e.addr]; !isRAM {
			continue
		}
		k := [2]uint32{e.pc, e.addr}
		if _, ok := seen[k]; !ok {
			seen[k] = e
		}
	}
	keys := make([][2]uint32, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][1] != keys[j][1] {
			return keys[i][1] < keys[j][1]
		}
		return keys[i][0] < keys[j][0]
	})
	for _, k := range keys {
		e := seen[k]
		fmt.Printf("  PC 0x%05X  READ 0x%06X = 0x%X  (%s)\n", e.pc, e.addr, e.val, e.label)
	}

	// Final resolved values of interest.
	fmt.Println("\n--- final RAM config-shadow values ---")
	fmt.Printf("  0xFFBF26 OPTMASK_A = 0x%08X\n", m.Bus.Read(0xFFBF26, bus.Long))
	fmt.Printf("  0xFFBF2A OPTMASK_B = 0x%08X\n", m.Bus.Read(0xFFBF2A, bus.Long))
	fmt.Printf("  0xFFB00C board_id  = 0x%04X  (=(OPTMASK_A>>19)&7)\n", m.Bus.Read(0xFFB00C, bus.Word))
	fmt.Printf("  0xFFBFEE IDNUM     = 0x%04X  (0x2191=8593)\n", m.Bus.Read(0xFFBFEE, bus.Word))
	fmt.Printf("  0xFFBF09 HPIB/RS232= 0x%02X    (4=HPIB,1/8=RS232)\n", m.Bus.Read(0xFFBF09, bus.Byte))
}
