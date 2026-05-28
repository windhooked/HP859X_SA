package hd63484

import (
	"image/color"
	"testing"
)

// pixel returns the RGB of c.img at (x, y).
func pixel(c *Chip, x, y int) color.RGBA {
	off := y*c.img.Stride + x*4
	return color.RGBA{c.img.Pix[off], c.img.Pix[off+1], c.img.Pix[off+2], c.img.Pix[off+3]}
}

func isLit(c *Chip, x, y int) bool {
	p := pixel(c, x, y)
	return p.R|p.G|p.B != 0
}

// feedWords pushes a sequence of 16-bit words through the chip data port.
func feedWords(c *Chip, words ...uint16) {
	for _, w := range words {
		c.WriteData(w)
	}
}

// TestStatusReady — the chip permanently reports all "ready" bits. Mirrors
// the polling-loop fidelity contract.
func TestStatusReady(t *testing.T) {
	c := New()
	s := c.ReadStatus()
	for _, bit := range []uint8{StatusCED, StatusARD, StatusWFR, StatusWFE} {
		if s&bit == 0 {
			t.Errorf("status %#02X missing ready bit %#02X", s, bit)
		}
	}
}

// TestAMOVE — absolute move updates the pen position.
func TestAMOVE(t *testing.T) {
	c := New()
	feedWords(c, cmdAMOVE, 100, 200)
	if c.penX != 100 || c.penY != 200 {
		t.Errorf("pen = (%d,%d), want (100,200)", c.penX, c.penY)
	}
	if c.Moves != 1 {
		t.Errorf("Moves=%d, want 1", c.Moves)
	}
}

// TestRMOVE — relative move adds to current pen.
func TestRMOVE(t *testing.T) {
	c := New()
	feedWords(c, cmdAMOVE, 100, 100)
	feedWords(c, cmdRMOVE, 10, 20)
	if c.penX != 110 || c.penY != 120 {
		t.Errorf("pen = (%d,%d), want (110,120)", c.penX, c.penY)
	}
	if c.Moves != 2 {
		t.Errorf("Moves=%d, want 2", c.Moves)
	}
}

// TestALINE — absolute line draws from pen to (endX, endY) and updates pen.
func TestALINE(t *testing.T) {
	c := New()
	feedWords(c, cmdAMOVE, 10, 10)
	feedWords(c, cmdALINE, 40, 10) // horizontal line
	for x := 10; x <= 40; x++ {
		if !isLit(c, x, 10) {
			t.Errorf("line pixel (%d,10) not lit", x)
		}
	}
	if c.penX != 40 || c.penY != 10 {
		t.Errorf("pen = (%d,%d) after ALINE, want (40,10)", c.penX, c.penY)
	}
	if c.Lines != 1 {
		t.Errorf("Lines=%d, want 1", c.Lines)
	}
}

// TestDOT — DOT plots a single pixel at the current pen.
func TestDOT(t *testing.T) {
	c := New()
	feedWords(c, cmdAMOVE, 50, 30)
	feedWords(c, cmdDOT)
	if !isLit(c, 50, 30) {
		t.Error("DOT did not light (50,30)")
	}
	if c.Dots != 1 {
		t.Errorf("Dots=%d, want 1", c.Dots)
	}
}

// TestARCT — absolute rectangle outline.
func TestARCT(t *testing.T) {
	c := New()
	feedWords(c, cmdAMOVE, 10, 10)
	feedWords(c, cmdARCT, 20, 15)
	for _, p := range [][2]int{{10, 10}, {20, 10}, {10, 15}, {20, 15}} {
		if !isLit(c, p[0], p[1]) {
			t.Errorf("corner (%d,%d) not lit", p[0], p[1])
		}
	}
	if c.Rects != 1 {
		t.Errorf("Rects=%d, want 1", c.Rects)
	}
}

// TestAFRCT — absolute filled rectangle.
func TestAFRCT(t *testing.T) {
	c := New()
	feedWords(c, cmdAMOVE, 30, 30)
	feedWords(c, cmdAFRCT, 33, 32)
	for y := 30; y <= 32; y++ {
		for x := 30; x <= 33; x++ {
			if !isLit(c, x, y) {
				t.Errorf("filled-rect pixel (%d,%d) not lit", x, y)
			}
		}
	}
	if c.FilledRects != 1 {
		t.Errorf("FilledRects=%d, want 1", c.FilledRects)
	}
}

// TestCRCL — circle draws an outline at +radius from pen.
func TestCRCL(t *testing.T) {
	c := New()
	feedWords(c, cmdAMOVE, 50, 50)
	feedWords(c, cmdCRCL, 10)
	// 4 cardinal points must be lit.
	for _, p := range [][2]int{{60, 50}, {40, 50}, {50, 60}, {50, 40}} {
		if !isLit(c, p[0], p[1]) {
			t.Errorf("circle cardinal (%d,%d) not lit", p[0], p[1])
		}
	}
}

// TestWPR — write parameter register sets a slot value + side effect for
// PRPenX / PRPenY (immediately moving the pen).
func TestWPR(t *testing.T) {
	c := New()
	// WPR PRPenX = 42, PRPenY = 17.
	feedWords(c, cmdWPRBase|PRPenX, 42)
	feedWords(c, cmdWPRBase|PRPenY, 17)
	if c.penX != 42 || c.penY != 17 {
		t.Errorf("pen after WPR = (%d,%d), want (42,17)", c.penX, c.penY)
	}
	if c.regs[PRPenX] != 42 || c.regs[PRPenY] != 17 {
		t.Errorf("regs after WPR = (%d,%d)", c.regs[PRPenX], c.regs[PRPenY])
	}
}

// TestRasterModeTrigger — writing PRMARLow=0x4000 then PRMARHigh=0x0000
// enters raster mode; subsequent data words go into VRAM.
func TestRasterModeTrigger(t *testing.T) {
	c := New()
	feedWords(c, cmdWPRBase|PRMARLow, 0x4000)
	feedWords(c, cmdWPRBase|PRMARHigh, 0x0000)
	// Now the parser is in stRasterData. Two pixel data words.
	feedWords(c, 0x4400, 0x4400)
	if c.PaintWords != 2 {
		t.Errorf("PaintWords=%d, want 2", c.PaintWords)
	}
	if c.Paints != 1 {
		t.Errorf("Paints=%d, want 1", c.Paints)
	}
	// vram[0..1] should hold 0x4400 little-endian.
	if c.vram[0] != 0x00 || c.vram[1] != 0x44 {
		t.Errorf("vram[0..1] = %02X %02X, want 00 44", c.vram[0], c.vram[1])
	}
}

// TestGlyphPacket — a WPTN with count=10 followed by fg+bg + 8 rows + 4
// trailer words paints an 8-row glyph at the pen.
func TestGlyphPacket(t *testing.T) {
	c := New()
	feedWords(c, cmdAMOVE, 10, 20)
	// WPTN header + count + colour + 8 rows (a vertical bar at col 0) + 4 trailer.
	feedWords(c, cmdWPTN, glyphWPTNCount, 0xFFFF, 0x0000)
	for i := 0; i < glyphRows; i++ {
		feedWords(c, 0x0001) // bit 0 set → leftmost pixel
	}
	feedWords(c, 0x0805, 0x0000, 0xD000, 0x0907) // trailer
	if c.Glyphs != 1 {
		t.Fatalf("Glyphs=%d, want 1", c.Glyphs)
	}
	// Vertical bar at penX=10. Pen positions the TOP-LEFT of the cell; rows
	// are stored bottom-up so row i lands at penY+(glyphRows-1-i). For an
	// all-bit-0-set glyph the result is an 8-pixel vertical bar covering
	// (10, 20..27).
	for i := 0; i < glyphRows; i++ {
		if !isLit(c, 10, 20+i) {
			t.Errorf("glyph pixel (10,%d) not lit", 20+i)
		}
	}
	// Pixel just left should stay background.
	if isLit(c, 9, 20) {
		t.Error("pixel (9,20) should be background")
	}
}

// TestUnknownCmd — unknown command opcodes are tallied + skipped without
// desyncing the parser (next valid command must dispatch normally).
func TestUnknownCmd(t *testing.T) {
	c := New()
	feedWords(c, 0x4003) // not a recognised command
	if c.UnknownCmds != 1 {
		t.Errorf("UnknownCmds=%d, want 1", c.UnknownCmds)
	}
	// Following AMOVE must still work.
	feedWords(c, cmdAMOVE, 11, 22)
	if c.penX != 11 || c.penY != 22 {
		t.Errorf("pen after recovery = (%d,%d), want (11,22)", c.penX, c.penY)
	}
}
