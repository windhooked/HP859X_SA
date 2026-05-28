// Command keymatrix2 is keymatrix but with multiple DriveOperatingTick
// cycles per attempt — testing whether the matrix dispatch happens
// across repeated tick iterations rather than within a single forced
// step run.
package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/machine"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

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
	byteIdx := 0
	bit := 0
	cycles := 20_000_000
	ticks := 10
	if len(os.Args) >= 3 {
		byteIdx, _ = strconv.Atoi(os.Args[1])
		bit, _ = strconv.Atoi(os.Args[2])
	}
	if len(os.Args) >= 5 {
		cycles, _ = strconv.Atoi(os.Args[3])
		ticks, _ = strconv.Atoi(os.Args[4])
	}
	fmt.Printf("Pressing matrix byte=%d bit=%d, then %d × DriveOperatingTick(%d)\n\n",
		byteIdx, bit, ticks, cycles)

	rom, err := romloader.LoadDir("hp8593a_eeproms")
	if err != nil {
		panic(err)
	}

	runOne := func(press bool) []byte {
		m, _ := machine.New8593A(rom)
		m.CPU.Reset()
		m.BootToOperating(30_000_000)
		if press {
			m.FrontPanel.SetBit(byteIdx, bit)
			// Fire IRQ3 so the handler sets bc67.0.
			m.CPU.SetIRQ(3)
			m.CPU.Run(400)
			m.CPU.SetIRQ(0)
		}
		// Drive multiple ticks — each runs the operating tick body and
		// returns to firmware idle in between.
		for t := 0; t < ticks; t++ {
			m.DriveOperatingTick(cycles)
		}
		return snapshot(m)
	}

	fmt.Println("baseline (no key) ...")
	basePost := runOne(false)
	fmt.Println("with key ...")
	post := runOne(true)

	witnessChanged := 0
	totalChanges := 0
	for i := uint32(0); i < ramSize; i++ {
		if basePost[i] != post[i] {
			totalChanges++
			addr := ramBase + i
			for _, w := range ipClearCells {
				if w == addr {
					fmt.Printf("  IP-witness %#06X  baseline=%#02X → with-key=%#02X\n",
						addr, basePost[i], post[i])
					witnessChanged++
				}
			}
		}
	}
	fmt.Printf("\n=== summary ===\n")
	fmt.Printf("  total cell deltas between baseline+key runs : %d\n", totalChanges)
	fmt.Printf("  IP-witness cells different                  : %d of %d\n",
		witnessChanged, len(ipClearCells))
}
