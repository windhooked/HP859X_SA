package hd63484

import (
	"image/png"
	"os"
	"strings"
	"testing"
)

// saveFrame renders the chip and writes it to screens/<name>.png (under the repo
// root, ../../../screens) for visual inspection. Best-effort; ignores errors so
// the test still asserts.
func saveFrame(c *Chip, name string) {
	name = strings.ReplaceAll(name, "/", "_")
	f, err := os.Create("../../../../screens/" + name + ".png")
	if err != nil {
		return
	}
	defer f.Close()
	// By-command coloured scanout with a legend strip (the canonical inspection
	// view): each lit pixel is tinted by the command that drew it.
	png.Encode(f, c.RenderScanoutByCmd())
}

// clearaccuracy_test.go is the clearing-accuracy spec: it draws each content
// type (glyphs, the reticule/graticule, a sweep trace) at known positions and
// clears it with the REAL firmware command path — area-definition rect (WPR
// 0x08-0x0b) + Read/Write Pointer (WPR 0x0c/0x0d) + SCLR (0x5C02) — then asserts
// the content was CLEANLY CLEARED. We do NOT reproduce the firmware's 1-bit
// dither (see area_ops.go "NO DITHERING"): the SCLR's checkerboard pattern word
// (0x5555/0xAAAA on real HW, fade-to-grey on the phosphor) is read as the intent
// "clear this region" and rendered as a clean foreground erase with nothing left
// in the background plane — regardless of the pattern word. These pin the
// SCLR↔content alignment across the screen.
//
// Coordinates are firmware drawing space. The SCLR is RWP-addressed at the
// EFFECTIVE origin (ORG_row - displayScanStart); wordByteAddr adds displayScanStart
// back so the clear lands on the pen-drawn content (see area_ops.go).

// sclrRect issues the firmware's SCLR over the firmware-coord rectangle
// (xmin,ymin)-(xmax,ymax) with the given AND pattern (0x5555 dither / 0x0000
// erase), exactly as the operating/cal display does.
func sclrRect(c *Chip, xmin, ymin, xmax, ymax int, pattern uint16) {
	setAreaDef(c, uint16(xmin), uint16(ymin), uint16(xmax), uint16(ymax))
	feedWords(c, 0x0800|PRMemWidth, 0xFFFF) // mask = all bits
	// RWP base = the CORE word offset of the rect's (xmin,ymin) corner via the SAME
	// addressing the pen uses (calcOffset = orgDPA + x/16 − y·mwr). Using the core
	// address — not the legacy orgRow/orgCol formula — makes the clear align with
	// drawn content at the EDGES (xmax column), not just the middle.
	off, _ := c.core.calcOffset(int16(xmin), int16(ymin))
	setRWP(c, off)
	ax := uint16((xmax-xmin)/16 + 1) // words across (over-cover; area-def clips)
	ay := uint16(ymax - ymin)        // rows up
	feedWords(c, 0x5C02, pattern, ax, ay)
}

// drawReticule draws a solid box + a couple of interior grid lines spanning the
// firmware rect — a stand-in for the graticule.
func drawReticule(c *Chip, xmin, ymin, xmax, ymax int) {
	feedWords(c, cmdAMOVE, uint16(xmin), uint16(ymin))
	feedWords(c, cmdALINE, uint16(xmax), uint16(ymin))
	feedWords(c, cmdALINE, uint16(xmax), uint16(ymax))
	feedWords(c, cmdALINE, uint16(xmin), uint16(ymax))
	feedWords(c, cmdALINE, uint16(xmin), uint16(ymin))
	midY := uint16((ymin + ymax) / 2)
	feedWords(c, cmdAMOVE, uint16(xmin), midY)
	feedWords(c, cmdALINE, uint16(xmax), midY)
}

// drawTrace draws a connected zig-zag polyline across the rect via ALINE — a
// stand-in for a sweep trace.
func drawTrace(c *Chip, xmin, ymin, xmax, ymax int) {
	feedWords(c, cmdAMOVE, uint16(xmin), uint16(ymin))
	up := true
	for x := xmin + 8; x <= xmax; x += 8 {
		y := ymin
		if up {
			y = ymax
		}
		up = !up
		feedWords(c, cmdALINE, uint16(x), uint16(y))
	}
}

// collectLit returns every firmware (x,y) in the rect that is currently lit.
func collectLit(c *Chip, x0, y0, x1, y1 int) [][2]int {
	var lit [][2]int
	for fy := y0; fy <= y1; fy++ {
		for fx := x0; fx <= x1; fx++ {
			if isLit(c, fx, fy) {
				lit = append(lit, [2]int{fx, fy})
			}
		}
	}
	return lit
}

// isLitBg checks the dim background plane (bgVram) at firmware (x,y).
func isLitBg(c *Chip, x, y int) bool { return c.isBgPixelLit(x, y) }

// assertCleared verifies the SCLR CLEANLY cleared the content: every previously-
// lit pixel is gone from the foreground (vram) AND nothing was left in the
// background plane (bgVram) — i.e. no dither dots. This is the same expectation
// for both the 0x5555 checkerboard and the 0x0000 erase, because we don't
// reproduce the 1-bit dither — both are read as "clear this region".
func assertCleared(t *testing.T, c *Chip, lit [][2]int) {
	t.Helper()
	if len(lit) == 0 {
		t.Fatal("no lit content to check")
	}
	for _, p := range lit {
		fx, fy := p[0], p[1]
		if isLit(c, fx, fy) {
			t.Errorf("foreground (%d,%d) should be cleanly cleared", fx, fy)
		}
		if isLitBg(c, fx, fy) {
			t.Errorf("background (%d,%d) should be empty — we don't dither", fx, fy)
		}
	}
}

// The reticule rect used across these tests (firmware coords, well inside vram).
const (
	retX0, retY0 = 16, 16
	retX1, retY1 = 208, 160
)

// runClearCase draws `draw`, captures the lit pixels in `box`, runs the SCLR over
// `box` with `pattern`, and applies `check`.
func runClearCase(t *testing.T, draw func(c *Chip), x0, y0, x1, y1 int, pattern uint16, check func(*testing.T, *Chip, [][2]int)) {
	c := New()
	draw(c)
	lit := collectLit(c, x0, y0, x1, y1)
	if len(lit) == 0 {
		t.Fatal("content was not drawn")
	}
	saveFrame(c, "clear_"+strings.ReplaceAll(t.Name(), "/", "_")+"_1drawn")
	sclrRect(c, x0, y0, x1, y1, pattern)
	saveFrame(c, "clear_"+strings.ReplaceAll(t.Name(), "/", "_")+"_2cleared")
	check(t, c, lit)
}

// 1. Glyphs OUTSIDE the reticule — a clear targeting that region works.
func TestClearGlyphsOutsideReticule(t *testing.T) {
	draw := func(c *Chip) {
		drawSolidGlyph(c, 260, 80)
		drawSolidGlyph(c, 300, 120)
	}
	box := [4]int{248, 72, 332, 136} // covers both outside glyphs
	t.Run("and5555", func(t *testing.T) {
		runClearCase(t, draw, box[0], box[1], box[2], box[3], 0x5555, assertCleared)
	})
	t.Run("erase", func(t *testing.T) {
		runClearCase(t, draw, box[0], box[1], box[2], box[3], 0x0000, assertCleared)
	})
}

// 2. The reticule (graticule box + grid) — clears.
func TestClearReticule(t *testing.T) {
	draw := func(c *Chip) { drawReticule(c, retX0, retY0, retX1, retY1) }
	t.Run("and5555", func(t *testing.T) {
		runClearCase(t, draw, retX0, retY0, retX1, retY1, 0x5555, assertCleared)
	})
	t.Run("erase", func(t *testing.T) {
		runClearCase(t, draw, retX0, retY0, retX1, retY1, 0x0000, assertCleared)
	})
}

// 3. A sweep trace inside the reticule — clears.
func TestClearTrace(t *testing.T) {
	draw := func(c *Chip) { drawTrace(c, retX0+8, retY0+8, retX1-8, retY1-8) }
	t.Run("and5555", func(t *testing.T) {
		runClearCase(t, draw, retX0, retY0, retX1, retY1, 0x5555, assertCleared)
	})
	t.Run("erase", func(t *testing.T) {
		runClearCase(t, draw, retX0, retY0, retX1, retY1, 0x0000, assertCleared)
	})
}

// TestClearTwoPassFullyClears verifies the screen ends up clean after the
// firmware's two complementary SCLR passes. On real HW these AND with 0x5555 then
// 0xAAAA (= AND 0x0000) to fade content over two frames; we don't dither, so the
// FIRST pass already cleans the region (we read SCLR as "clear") and the second
// is a no-op on already-cleared content. Either way the end state is blank —
// the user's "it should clear, not stay dithered" intuition, satisfied in one
// pass instead of two.
func TestClearTwoPassFullyClears(t *testing.T) {
	c := New()
	drawReticule(c, retX0, retY0, retX1, retY1)
	drawSolidGlyph(c, retX0+64, retY0+48)
	lit := collectLit(c, retX0, retY0, retX1, retY1)
	if len(lit) == 0 {
		t.Fatal("content not drawn")
	}
	saveFrame(c, "clear_twopass_1drawn")
	sclrRect(c, retX0, retY0, retX1, retY1, 0x5555) // pass 1 → already clean
	saveFrame(c, "clear_twopass_2afterpass1")
	sclrRect(c, retX0, retY0, retX1, retY1, 0xAAAA) // pass 2 (complement) → still clean
	saveFrame(c, "clear_twopass_3afterpass2")
	assertCleared(t, c, lit) // after either/both passes, everything is gone
}

// 4. Glyphs INSIDE the reticule — clears.
func TestClearGlyphsInsideReticule(t *testing.T) {
	draw := func(c *Chip) {
		drawSolidGlyph(c, retX0+32, retY0+32)
		drawSolidGlyph(c, retX0+96, retY0+64)
		drawSolidGlyph(c, retX1-48, retY1-48)
	}
	t.Run("and5555", func(t *testing.T) {
		runClearCase(t, draw, retX0, retY0, retX1, retY1, 0x5555, assertCleared)
	})
	t.Run("erase", func(t *testing.T) {
		runClearCase(t, draw, retX0, retY0, retX1, retY1, 0x0000, assertCleared)
	})
}
