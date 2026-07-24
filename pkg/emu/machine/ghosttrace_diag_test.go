package machine

import (
	"os"
	"testing"

	"github.com/windhooked/HP859X_SA/pkg/emu/romloader"
)

// TestGhostTraceDiag — two-pass ghost-pixel lifecycle probe:
// pass 1 finds a ghost word (lit, even-phase-only, in the graph) after a long
// natural run; pass 2 reruns with the write-history watch on that word and
// dumps every write (old, new, cmd-tag) — establishing who drew it and whether
// any erase op ever touched it. DIAG=1.
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func TestGhostTraceDiag(t *testing.T) {
	if os.Getenv("DIAG") == "" {
		t.Skip("DIAG=1")
	}
	rom, err := romloader.LoadDir("../../../hp8593a_eeproms")
	if err != nil {
		t.Skip("rom")
	}
	run := func(watch uint32, log *[][3]uint16) *Machine {
		m, _ := New8593A(rom)
		if log != nil {
			m.MMIO.Display.Chip.WatchCoreWord(watch, log)
		}
		m.CPU.Reset()
		m.MMIO.SweepActive = true
		m.SweepDrive = true
		m.BootToOperatingWithSweep(250_000_000)
		for i := 0; i < 150; i++ {
			m.bootLoop(2_000_000, nil)
		}
		return m
	}
	// Pass 1: locate ghost words.
	m := run(0, nil)
	chip := m.MMIO.Display.Chip
	type gw struct {
		off uint32
		v   uint16
	}
	var ghosts []gw
	for row := 30; row <= 230; row++ {
		for wc := 6; wc <= 29; wc++ {
			off := uint32(row*64 + wc)
			v := chip.CoreWord(off)
			if v == 0 {
				continue
			}
			// GLYPH-tagged lit pixels only — the text ghosts (the even-phase
			// filter alone catches the healthy 0x1111 grid stipple).
			glyph := false
			for b := 0; b < 16; b++ {
				if v&(1<<uint(b)) != 0 && chip.CoreTagBit(off, b) == 7 {
					glyph = true
					break
				}
			}
			if glyph {
				ghosts = append(ghosts, gw{off, v})
			}
		}
	}
	t.Logf("pass1: %d glyph-tagged lit words in graph", len(ghosts))
	if len(ghosts) == 0 {
		return
	}
	// Pick one from the middle of the pack (a band word, not a box edge).
	pick := ghosts[len(ghosts)/2]
	t.Logf("watching word %#x (row %d col %d, value %#04x)", pick.off, pick.off/64, pick.off%64, pick.v)

	// Pass 2: rerun with the watch + AAAA-rect coverage log.
	var log [][3]uint16
	var rects [][3]int
	m2, _ := New8593A(rom)
	m2.MMIO.Display.Chip.WatchCoreWord(pick.off, &log)
	m2.MMIO.Display.Chip.AAAARectLog = &rects
	m2.CPU.Reset()
	m2.MMIO.SweepActive = true
	m2.SweepDrive = true
	m2.BootToOperatingWithSweep(250_000_000)
	for i := 0; i < 150; i++ {
		m2.bootLoop(2_000_000, nil)
	}
	t.Logf("pass2: %d writes to the watched word; %d AAAA rects executed", len(log), len(rects))
	// Does ANY AAAA rect cover the watched word? Rect spans words
	// [rwp-ay*64 .. rwp] rows x [rwp .. rwp+ax] cols (execClear addressing).
	hits := 0
	near := 0
	for _, r := range rects {
		base := r[0]
		for d1 := 0; d1 <= r[2]; d1++ {
			for d0 := 0; d0 <= r[1]; d0++ {
				off := base - d1*64 + d0
				if uint32(off) == pick.off {
					hits++
				}
				if abs(off-int(pick.off)) <= 2 {
					near++
				}
			}
		}
	}
	t.Logf("AAAA-rect coverage of watched word %#x: exact=%d near(±2 words)=%d", pick.off, hits, near)
	if len(rects) > 0 {
		show := rects
		if len(show) > 12 {
			show = show[:12]
		}
		for _, r := range show {
			t.Logf("  AAAA rect rwp=%#x (row %d col %d) ax=%d ay=%d", r[0], r[0]/64, r[0]%64, r[1], r[2])
		}
	}
	show := log
	tagName := []string{"none", "poly", "sclr", "clr", "rect", "line", "dot", "glyph", "raster", "other"}
	for i, e := range show {
		tn := "?"
		if int(e[2]) < len(tagName) {
			tn = tagName[e[2]]
		}
		t.Logf("  w%02d: %04X -> %04X  tag=%s", i, e[0], e[1], tn)
	}
}
