// Command crtdiag is a DIAGNOSTIC (read-only) probe for the HD63484 CRT-driver
// geometry. It boots to the operating UI, captures every drawLine the firmware
// emits, and reports the coordinate system the firmware actually uses for the
// graticule + grid: the overall bounding box, the longest axis-aligned segments
// (the graticule frame), and the distinct vertical/horizontal grid pitches.
//
// Purpose: root-cause why our render places the graticule half-size in the
// top-left (plus a duplicate box) instead of filling/centering the 640×480
// screen like the real 8593E. If the firmware's coordinates already fill a
// 640×480-ish box, the bug is in our decode; if they cluster in a sub-region or
// exceed the screen, the bug is a missing coordinate transform (ORG / display-
// base / zoom registers the chip applies on real hardware but our model omits).
package main

import (
	"fmt"
	"sort"

	"github.com/windhooked/HP859X_SA/pkg/emu/machine"
	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

func main() {
	rom, err := romloader.LoadDir("hp8593a_eeproms")
	if err != nil {
		panic(err)
	}
	m, err := machine.New8593A(rom)
	if err != nil {
		panic(err)
	}
	m.CPU.Reset()
	m.MMIO.Display.EnableLineLog()
	m.BootToOperating(200_000_000)

	ll := m.MMIO.Display.LineLog
	fmt.Printf("== crtdiag: %d drawLine calls captured ==\n\n", len(ll))
	if len(ll) == 0 {
		fmt.Println("no lines drawn — UI graticule path not reached")
		return
	}

	// Overall bounding box.
	minX, minY, maxX, maxY := 1<<30, 1<<30, -(1 << 30), -(1 << 30)
	bump := func(x, y int) {
		if x < minX {
			minX = x
		}
		if y < minY {
			minY = y
		}
		if x > maxX {
			maxX = x
		}
		if y > maxY {
			maxY = y
		}
	}
	type seg struct{ x0, y0, x1, y1 int }
	freq := map[seg]int{}
	var vert, horiz, diag int
	vx := map[int]int{} // x of vertical segments → count
	hy := map[int]int{} // y of horizontal segments → count
	for _, r := range ll {
		bump(r.X0, r.Y0)
		bump(r.X1, r.Y1)
		freq[seg{r.X0, r.Y0, r.X1, r.Y1}]++
		switch {
		case r.X0 == r.X1:
			vert++
			vx[r.X0]++
		case r.Y0 == r.Y1:
			horiz++
			hy[r.Y0]++
		default:
			diag++
		}
	}
	fmt.Printf("coordinate bounding box: x %d..%d  (span %d)   y %d..%d  (span %d)\n",
		minX, maxX, maxX-minX, minY, maxY, maxY-minY)
	fmt.Printf("screen is 640x480; visible paint window 1024x512\n")
	fmt.Printf("segments: %d vertical, %d horizontal, %d diagonal, %d distinct\n\n",
		vert, horiz, diag, len(freq))

	// Distinct vertical grid X positions (the graticule columns).
	var xs []int
	for x := range vx {
		xs = append(xs, x)
	}
	sort.Ints(xs)
	fmt.Printf("vertical-segment X positions (x: count): ")
	for _, x := range xs {
		fmt.Printf("%d:%d  ", x, vx[x])
	}
	fmt.Println()
	if len(xs) > 1 {
		fmt.Printf("  → X range %d..%d, %d distinct columns, pitch ~%d\n",
			xs[0], xs[len(xs)-1], len(xs), (xs[len(xs)-1]-xs[0])/maxInt(1, len(xs)-1))
	}

	var ys []int
	for y := range hy {
		ys = append(ys, y)
	}
	sort.Ints(ys)
	fmt.Printf("horizontal-segment Y positions (y: count): ")
	for _, y := range ys {
		fmt.Printf("%d:%d  ", y, hy[y])
	}
	fmt.Println()
	if len(ys) > 1 {
		fmt.Printf("  → Y range %d..%d, %d distinct rows, pitch ~%d\n",
			ys[0], ys[len(ys)-1], len(ys), (ys[len(ys)-1]-ys[0])/maxInt(1, len(ys)-1))
	}

	// Top distinct segments by frequency (the persistent graticule frame).
	type fe struct {
		s seg
		n int
	}
	var fes []fe
	for s, n := range freq {
		fes = append(fes, fe{s, n})
	}
	sort.Slice(fes, func(i, j int) bool { return fes[i].n > fes[j].n })
	fmt.Println("\ntop 24 distinct segments (x0,y0)->(x1,y1) ×count:")
	for i := 0; i < len(fes) && i < 24; i++ {
		s := fes[i].s
		fmt.Printf("  (%4d,%4d)->(%4d,%4d) ×%d\n", s.x0, s.y0, s.x1, s.y1, fes[i].n)
	}

	// HD63484 display-control / partition registers — the VRAM→screen mapping
	// the firmware programs (and our renderer ignores). If the upper/lower
	// screen base addresses differ, the chip is in split-screen mode (which
	// explains the two graticule boxes).
	d := m.MMIO.Display
	names := map[int]string{
		0x04: "MEMWIDTH", 0x12: "UPPER_BASE", 0x13: "UPPER_WIDTH",
		0x14: "BASE_BASE", 0x15: "BASE_WIDTH", 0x16: "LOWER_BASE", 0x17: "LOWER_WIDTH",
		0x18: "WINDOW_BASE", 0x19: "WINDOW_WIDTH", 0x1B: "HORZ_SCROLL", 0x1C: "VERT_SCROLL",
		0x02: "DISP_RX", 0x03: "DISP_RY",
	}
	// ORG (set-drawing-origin) commands — the offset the chip adds to draw
	// coords on real hardware (which our renderer ignores).
	orgs := d.OrgLog
	ofreq := map[[2]int]int{}
	for _, o := range orgs {
		ofreq[o]++
	}
	fmt.Printf("\nORG commands: %d total, distinct origins (x,y)×count: ", len(orgs))
	for o, n := range ofreq {
		fmt.Printf("(%d,%d)×%d  ", o[0], o[1], n)
	}
	fmt.Println()

	fmt.Println("\nHD63484 display-control / partition registers:")
	order := []int{0x02, 0x03, 0x04, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18, 0x19, 0x1B, 0x1C}
	for _, r := range order {
		fmt.Printf("  PR%02X %-12s = 0x%04X\n", r, names[r], d.Reg(r))
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
