// dispstream: show AMOVE positions immediately preceding APLL/RPLL polylines,
// to determine whether the small polyline vertices are absolute or pen-relative.
package main

import (
	"fmt"

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
		if a == 0xFFF5FE && sz == bus.Word {
			s = append(s, uint16(v))
		}
	}
	m.BootToOperating(200_000_000)
	penX, penY := 0, 0
	shown := 0
	i := 0
	for i < len(s) && shown < 12 {
		ww := s[i]
		_ = ww
		n, kind := frame(s, i)
		switch kind {
		case "MOVE":
			penX, penY = int(int16(s[i+1])), int(int16(s[i+2]))
		case "APLL", "RPLL":
			cnt := int(s[i+1])
			fmt.Printf("%s N=%d pen=(%d,%d) v0=(%d,%d) v1=(%d,%d) vlast=(%d,%d)\n",
				kind, cnt, penX, penY,
				int16(s[i+2]), int16(s[i+3]),
				int16(s[i+4]), int16(s[i+5]),
				int16(s[i+2+2*(cnt-1)]), int16(s[i+3+2*(cnt-1)]))
			shown++
		}
		if n < 0 {
			i++
			continue
		}
		i += 1 + n
	}
}

func frame(s []uint16, i int) (int, string) {
	w := s[i]
	_ = w
	if w&0xFFE0 == 0x0800 {
		return 1, "WPR"
	}
	if w&0xFFE0 == 0x0C00 {
		return 0, "RPR"
	}
	switch w & 0xFC00 {
	case 0x9800:
		if i+1 < len(s) {
			return 1 + 2*int(s[i+1]), "APLL"
		}
	case 0x9C00:
		if i+1 < len(s) {
			return 1 + 2*int(s[i+1]), "RPLL"
		}
	}
	if w&0xFFF8 == 0x5C00 {
		return 3, "SCLR"
	}
	if w == 0x1800 && i+1 < len(s) {
		if int(s[i+1]) == 0x000A {
			return 15, "WPTNg"
		}
		return 1 + int(s[i+1]), "WPTN"
	}
	switch w {
	case 0x0000:
		return 2, "ORG"
	case 0x8000, 0x8400:
		return 2, "MOVE"
	case 0x8800, 0x8801, 0x8C00, 0x8C01:
		return 2, "LINE"
	case 0x9000, 0x9001, 0x9400, 0x9401:
		return 2, "RCT"
	case 0xA000, 0xA001, 0xA400, 0xA401:
		return 2, "FRCT"
	case 0xCC00, 0xCC01:
		return 0, "DOT"
	case 0xC000:
		return 1, "CRCL"
	case 0xE000:
		return 1, "PAINT"
	case 0xF400, 0xF401:
		return 1, "SCLRf"
	case 0xF000, 0xF001:
		return 3, "CLR"
	case 0xF800, 0xF801, 0xFC00, 0xFC01:
		return 4, "CPY"
	case 0x5800:
		return 3, "BLK"
	}
	return -1, "?"
}
