// Command linecheck identifies the diagonal line in the bottom-left of the
// graticule (the user-spotted "spectrum line above 0 dBm"). It boots, enables
// the HD63484 chip's per-line endpoint capture, runs a display refresh, and
// reports every drawLine whose endpoints land in the lower-left graticule
// region -- distinguishing a data trace (many short irregular segments) from a
// single vector (UI/blanked-trace artifact).
package main

import (
	"fmt"
	"sort"

	"github.com/windhooked/HP859X_SA/internal/emutest"
	"github.com/windhooked/HP859X_SA/pkg/emu/cpu"
	"github.com/windhooked/HP859X_SA/pkg/emu/machine"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

func main() {
	rom, _ := romloader.LoadDir("hp8593a_eeproms")
	m, _ := machine.New8593A(rom)
	m.CPU.Reset()
	lb := emutest.NewLoopBreaker(50)
	m.MMIO.Display.EnableLineLog()
	for done := 0; done < 160_000_000; done += 2000 {
		m.CPU.Run(2000)
		lb.Check(m.CPU.Reg(cpu.PC), m.CPU.SetReg)
		if (done/2000)%5 == 0 {
			m.CPU.SetIRQ(5)
			m.CPU.Run(400)
			m.CPU.SetIRQ(0)
		}
	}
	ll := m.MMIO.Display.LineLog
	// Count by endpoint, and list lines touching the lower-left (x<110, y>90).
	type seg struct{ x0, y0, x1, y1 int }
	freq := map[seg]int{}
	for _, r := range ll {
		freq[seg{r.X0, r.Y0, r.X1, r.Y1}]++
	}
	var ll2 []struct {
		s seg
		n int
	}
	for s, n := range freq {
		ll2 = append(ll2, struct {
			s seg
			n int
		}{s, n})
	}
	sort.Slice(ll2, func(i, j int) bool { return ll2[i].n > ll2[j].n })
	fmt.Printf("captured %d drawLine calls, %d distinct segments\n", len(ll), len(freq))
	fmt.Println("segments touching the lower-left graticule (x<110 && y in 90..230), by frequency:")
	shown := 0
	for _, e := range ll2 {
		s := e.s
		diag := s.x0 != s.x1 && s.y0 != s.y1
		dx := s.x1 - s.x0
		if dx < 0 {
			dx = -dx
		}
		dy := s.y1 - s.y0
		if dy < 0 {
			dy = -dy
		}
		if diag && dx > 3 && dy > 3 {
			fmt.Printf("  (%4d,%4d)->(%4d,%4d) x%d  DIAGONAL\n", s.x0, s.y0, s.x1, s.y1, e.n)
			shown++
			if shown > 25 {
				break
			}
		}
	}
}
