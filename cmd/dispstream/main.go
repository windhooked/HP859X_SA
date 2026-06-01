// dispstream: capture the HD63484 Address-Register protocol — 0xFFF5FC (AR) + the
// following 0xFFF5FE (data) writes — to find the DISPLAY-START register (SAR/RAR)
// and whether the firmware page-flips it. AR=0 means command-FIFO (drawing);
// AR!=0 addresses a control register (auto-increments by +2 per data word).
package main

import (
	"fmt"
	"sort"

	"github.com/windhooked/HP859X_SA/pkg/emu/bus"
	"github.com/windhooked/HP859X_SA/pkg/emu/machine"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

func main() {
	rom, _ := romloader.LoadDir("hp8593a_eeproms")
	m, _ := machine.New8593A(rom)
	m.CPU.Reset()
	var ar uint16          // current Address Register
	regWrites := map[uint16][]uint16{} // AR -> sequence of values written
	m.Bus.OnWrite = func(a uint32, sz bus.Size, v uint32) {
		if sz != bus.Word { return }
		switch a {
		case 0xFFF5FC:
			ar = uint16(v)
		case 0xFFF5FE:
			if ar != 0 { // control-register write (not command FIFO)
				if len(regWrites[ar]) < 200 { regWrites[ar] = append(regWrites[ar], uint16(v)) }
				ar += 2 // auto-increment
			}
		}
	}
	m.BootToOperating(200_000_000)

	// Report control-register ARs touched, with distinct values (focus on the
	// display-address registers: RAR/SAR area, AR 0xC8..0xCF).
	var ars []int
	for a := range regWrites { ars = append(ars, int(a)) }
	sort.Ints(ars)
	fmt.Println("AR -> distinct values (count) [display regs are ~0xC8..0xCF, 0x80..0x9F raster]:")
	for _, a := range ars {
		vs := regWrites[uint16(a)]
		dist := map[uint16]int{}
		for _, v := range vs { dist[v]++ }
		isDisp := a >= 0xC0 && a <= 0xDF // display-address regs (RAR/MWR/SAR/base/upper/lower)
		if !isDisp && len(dist) <= 1 && len(vs) < 5 { continue }
		// build distinct summary
		var ks []int
		for k := range dist { ks = append(ks, int(k)) }
		sort.Ints(ks)
		s := ""
		for _, k := range ks { s += fmt.Sprintf("%04X×%d ", k, dist[uint16(k)]) }
		fmt.Printf("  AR=%02X (%d writes, %d distinct): %s\n", a, len(vs), len(dist), s)
	}
}
