package hd63484

import (
	"bufio"
	"image/png"
	"os"
	"strconv"
	"testing"
)

// loadCaptureWords reads a screens/crt_*.txt hex-per-line capture into words.
func loadCaptureWords(t *testing.T, rel string) []uint16 {
	t.Helper()
	f, err := os.Open("../../../../" + rel)
	if err != nil {
		t.Skipf("capture not present: %v", err)
	}
	defer f.Close()
	var words []uint16
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, err := strconv.ParseUint(sc.Text(), 16, 16); err == nil {
			words = append(words, uint16(v))
		}
	}
	return words
}

// TestProbeHPLogo replays the firmware command capture and dumps the FIRST batch
// of polyline/line segments (which build the static chrome — the top-left "hp"
// logo and the graph frame) with their firmware coordinates, plus the active
// area-definition clip rect. It flags which of those early segments fall OUTSIDE
// the area-def rect — i.e. which ones the AREA-mode clip (opcode bit 6) would
// chop, producing the "discontinued" look. Diagnostic only.
func TestProbeHPLogo(t *testing.T) {
	words := loadCaptureWords(t, "screens/crt_20260603_134246.txt")
	if len(words) == 0 {
		t.Skip("empty capture")
	}

	c := New()
	c.EnableLineLog()
	c.EnableDotLog()

	// Manually decode AMOVE / APLL headers so we can tag which segments belong to
	// which command and whether that command had the AREA bit (0x40) set.
	type cmdMark struct {
		idx  int // first LineLog index this command produced
		op   uint16
		area bool
	}
	var marks []cmdMark
	st := 0 // 0=cmd, else parameter-consume countdown handled inline
	_ = st

	// Replay through the real chip (so the segments are exactly what it draws),
	// but also keep our own lightweight opcode tag by re-scanning the stream.
	prevLines := 0
	// Build a parallel decode to attribute LineLog ranges to opcodes.
	// Simpler: just replay, then dump. We separately scan opcodes below.
	for _, w := range words {
		c.WriteData(w)
		if len(c.LineLog) != prevLines {
			prevLines = len(c.LineLog)
		}
	}

	xmin := int(int16(c.regs[0x08]))
	ymin := int(int16(c.regs[0x09]))
	xmax := int(int16(c.regs[0x0a]))
	ymax := int(int16(c.regs[0x0b]))
	t.Logf("area-def rect (regs 0x08-0x0b): x[%d..%d] y[%d..%d]", xmin, xmax, ymin, ymax)
	t.Logf("total line segments=%d dots=%d", len(c.LineLog), len(c.DotLog))

	inRect := func(x, y int) bool { return x >= xmin && x <= xmax && y >= ymin && y <= ymax }

	// Region histogram: where do all segments live?
	var topLeft, topBand, graph, other int
	minY, maxY := 1<<30, -(1 << 30)
	for _, ln := range c.LineLog {
		for _, y := range []int{ln.Y0, ln.Y1} {
			if y < minY {
				minY = y
			}
			if y > maxY {
				maxY = y
			}
		}
		switch {
		case ln.X0 < 100 && ln.Y0 > 150:
			topLeft++
		case ln.Y0 > 150:
			topBand++
		case inRect(ln.X0, ln.Y0):
			graph++
		default:
			other++
		}
	}
	t.Logf("Y range of all segments: [%d..%d]", minY, maxY)
	t.Logf("regions: topLeft(x<100,y>150)=%d  topBand(y>150)=%d  graph=%d  other=%d",
		topLeft, topBand, graph, other)

	t.Log("--- segments with Y>150 (top status band incl. logo), first 80 ---")
	n := 0
	for i, ln := range c.LineLog {
		if ln.Y0 <= 150 && ln.Y1 <= 150 {
			continue
		}
		a := inRect(ln.X0, ln.Y0)
		b := inRect(ln.X1, ln.Y1)
		flag := ""
		if !a || !b {
			flag = "  <-- OUTSIDE area-def (AREA-clip would chop)"
		}
		t.Logf("seg %3d  (%4d,%4d)->(%4d,%4d)%s", i, ln.X0, ln.Y0, ln.X1, ln.Y1, flag)
		if n++; n >= 80 {
			break
		}
	}

	t.Log("--- all dots ---")
	for i, d := range c.DotLog {
		t.Logf("dot %2d  (%4d,%4d)", i, d.X, d.Y)
	}

	// Render this replay's core by-command + scanout, so we can see whether the
	// top-left "italic h" is present in THIS steady-state frame or only at boot.
	if f, err := os.Create("../../../../screens/replay_bycmd.png"); err == nil {
		png.Encode(f, c.RenderByCmd(0))
		f.Close()
		t.Log("wrote screens/replay_bycmd.png")
	}
	if f, err := os.Create("../../../../screens/replay_scanout.png"); err == nil {
		png.Encode(f, c.RenderScanout())
		f.Close()
		t.Log("wrote screens/replay_scanout.png")
	}

	_ = marks
}
