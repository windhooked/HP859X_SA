// dispstream: command-type histogram + CPY/SCPY coords + LINE Y distribution, to
// determine how the back-buffer frame (Y240..440) and front grid (Y5..196) combine.
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
	var s []uint16
	m.Bus.OnWrite = func(a uint32, sz bus.Size, v uint32) {
		if a == 0xFFF5FE && sz == bus.Word { s = append(s, uint16(v)) }
	}
	m.BootToOperating(200_000_000)
	hist := map[string]int{}
	lineY := map[int]int{}
	cpy := map[string]int{}
	px, py := 0, 0
	reg := map[uint16]uint16{}
	i := 0
	for i < len(s) {
		n, kind := frame(s, i)
		hist[kind]++
		switch kind {
		case "WPR":
			r := s[i]&0x1F
			reg[r]=s[i+1]
			if r==0x06 { px=int(int16(s[i+1])) }
			if r==0x07 { py=int(int16(s[i+1])) }
		case "MOVE":
			px, py = int(int16(s[i+1])), int(int16(s[i+2]))
		case "LINE":
			lineY[py]++
			py = int(int16(s[i+2])) // pen moves to line end
		case "CPY":
			cpy[fmt.Sprintf("srcMAR=%04X:%04X dx=%d dy=%d penbuf=(%d,%d) R0C=%04X R0D=%04X", s[i+1],s[i+2],int(int16(s[i+3])),int(int16(s[i+4])),px,py,reg[0x0C],reg[0x0D])]++
		}
		if n < 0 { i++; continue }
		i += 1 + n
	}
	fmt.Println("command-type histogram:")
	var ks []string
	for k := range hist { ks=append(ks,k) }
	sort.Strings(ks)
	for _, k := range ks { fmt.Printf("  %-8s %d\n", k, hist[k]) }
	fmt.Println("\nLINE pen-Y distribution (Y×count, >20):")
	var ys []int
	for y := range lineY { ys=append(ys,y) }
	sort.Ints(ys)
	for _, y := range ys { if lineY[y]>20 { fmt.Printf("  Y=%d ×%d\n", y, lineY[y]) } }
	fmt.Println("\nCPY/SCPY commands:")
	for k, c := range cpy { fmt.Printf("  %s ×%d\n", k, c) }
}

func frame(s []uint16, i int) (int, string) {
	w := s[i]
	if w&0xFFE0 == 0x0800 { return 1, "WPR" }
	if w&0xFFE0 == 0x0C00 { return 0, "RPR" }
	switch w & 0xFC00 {
	case 0x9800: if i+1 < len(s) { return 1 + 2*int(s[i+1]), "APLL" }
	case 0x9C00: if i+1 < len(s) { return 1 + 2*int(s[i+1]), "RPLL" }
	}
	if w&0xFFF8 == 0x5C00 { return 3, "SCLR5" }
	if w == 0x1800 && i+1 < len(s) { if int(s[i+1])==0x000A { return 15, "WPTNg" }; return 1+int(s[i+1]), "WPTN" }
	switch w {
	case 0x0000: return 2, "ORG"
	case 0x8000, 0x8400: return 2, "MOVE"
	case 0x8800, 0x8801, 0x8C00, 0x8C01: return 2, "LINE"
	case 0x9000, 0x9001, 0x9400, 0x9401: return 2, "RCT"
	case 0xA000, 0xA001, 0xA400, 0xA401: return 2, "FRCT"
	case 0xCC00, 0xCC01: return 0, "DOT"
	case 0xC000: return 1, "CRCL"
	case 0xE000: return 1, "PAINT"
	case 0xF400, 0xF401: return 1, "SCLRf"
	case 0xF000, 0xF001: return 3, "CLR"
	case 0xF800, 0xF801, 0xFC00, 0xFC01: return 4, "CPY"
	case 0x5800: return 3, "BLK"
	}
	return -1, "?"
}
