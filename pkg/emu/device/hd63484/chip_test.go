package hd63484

import (
	"image/color"
	"testing"
)

// pixel returns the rendered RGBA at (x, y), materialising the framebuffer
// from VRAM on demand. VRAM is the single source of truth in the unified
// chip model; the RGBA framebuffer is derived from it by RenderFrame.
func pixel(c *Chip, x, y int) color.RGBA {
	img := c.RenderFrame()
	off := y*img.Stride + x*4
	return color.RGBA{img.Pix[off], img.Pix[off+1], img.Pix[off+2], img.Pix[off+3]}
}

// isLit reports whether VRAM has bit (x, y) set — equivalent to "the pixel
// at (x, y) would be rendered in fgColor". Checks VRAM directly to avoid
// the per-call full-frame render.
func isLit(c *Chip, x, y int) bool {
	return c.isVRAMPixelLit(x, y)
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
// arms raster mode (the empirical screen-fill trigger); subsequent data
// words go into VRAM.
func TestRasterModeTrigger(t *testing.T) {
	c := New()
	feedWords(c, cmdWPRBase|PRMARLow, 0x4000)
	feedWords(c, cmdWPRBase|PRMARHigh, 0x0000)
	feedWords(c, 0x4400, 0x4400)
	if c.PaintWords != 2 {
		t.Errorf("PaintWords=%d, want 2", c.PaintWords)
	}
	if c.Paints != 1 {
		t.Errorf("Paints=%d, want 1", c.Paints)
	}
	// vram[0..1] should hold 0x4400 little-endian.
	// Raster bursts are the firmware's faint background dot texture, so they
	// land in the BACKGROUND plane (bgVram), not the bright foreground vram.
	if c.bgVram[0] != 0x00 || c.bgVram[1] != 0x44 {
		t.Errorf("bgVram[0..1] = %02X %02X, want 00 44", c.bgVram[0], c.bgVram[1])
	}
	if c.vram[0] != 0x00 || c.vram[1] != 0x00 {
		t.Errorf("vram[0..1] = %02X %02X, want 00 00 (raster must not touch foreground)", c.vram[0], c.vram[1])
	}
}

// TestSCLR — screen clear fills all of VRAM with the supplied word and
// resets the drawn-content bounding box.
func TestSCLR(t *testing.T) {
	c := New()
	// First light a pixel so the bbox tracks (10, 20).
	feedWords(c, cmdAMOVE, 10, 20)
	feedWords(c, cmdDOT)
	if !isLit(c, 10, 20) {
		t.Fatal("DOT setup failed")
	}
	// SCLR with 0x0000 should clear everything.
	feedWords(c, cmdSCLR, 0x0000)
	if isLit(c, 10, 20) {
		t.Error("pixel (10,20) still lit after SCLR 0x0000")
	}
	if c.ScreenClears != 1 {
		t.Errorf("ScreenClears=%d, want 1", c.ScreenClears)
	}
	// SCLR with all-ones should light every VRAM byte.
	feedWords(c, cmdSCLR, 0xFFFF)
	for i, b := range c.vram[:16] {
		if b != 0xFF {
			t.Errorf("vram[%d] = %02X after SCLR 0xFFFF, want FF", i, b)
		}
	}
}

// TestGlyphBlitOpaque — a glyph blit is OPAQUE within its 8×16 cell: FG bits
// light, and the cell's non-lit pixels are CLEARED (in the foreground plane).
// This is what lets a re-blitted glyph — e.g. a blinking annunciator redrawn
// at the same cell — overwrite the previous one instead of accumulating into
// an unreadable overlap. (The earlier "BG=0 is transparent" model left stale
// pixels and produced garbled annunciators like "ADC-2VMEAFBIL"; the dim
// background dot texture still shows through cleared cells because it lives in
// the separate bgVram plane.)
func TestGlyphBlitOpaque(t *testing.T) {
	c := New()
	// Pre-light pixel (25, 33) INSIDE the target cell — must be cleared.
	c.setVRAMPixel(25, 33)
	// And one OUTSIDE the cell — must be untouched.
	c.setVRAMPixel(40, 33)
	feedWords(c, cmdAMOVE, 20, 30)
	feedWords(c, cmdWPTN, glyphWPTNCount, 0x0000, 0x0000)
	for i := 0; i < glyphRows; i++ {
		feedWords(c, 0x0001) // only bit 0 (column 0) set
	}
	feedWords(c, 0x0805, 0x0000, 0xD000, 0x0907)
	// Column 0 of the cell (x=20) should be lit.
	for y := 30; y < 38; y++ {
		if !isLit(c, 20, y) {
			t.Errorf("glyph FG pixel (20,%d) not lit", y)
		}
	}
	// The non-lit pixel (25,33) inside the cell is cleared (opaque).
	if isLit(c, 25, 33) {
		t.Error("non-lit cell pixel (25, 33) should be cleared by the opaque glyph blit")
	}
	// A pixel outside the 8×16 cell is untouched.
	if !isLit(c, 40, 33) {
		t.Error("pixel (40, 33) outside the glyph cell should be untouched")
	}
}

// TestGlyphBGNonZeroFillsCell — when BG is explicitly non-zero the chip
// fills the row-clear pixels in the cell (per HD63484 fill semantics).
// No observed firmware path uses this, but the model honours it for
// completeness.
func TestGlyphBGNonZeroFillsCell(t *testing.T) {
	c := New()
	feedWords(c, cmdAMOVE, 100, 50)
	feedWords(c, cmdWPTN, glyphWPTNCount, 0x0000, 0xFFFF)
	for i := 0; i < glyphRows; i++ {
		feedWords(c, 0x0000) // all rows blank
	}
	feedWords(c, 0x0805, 0x0000, 0xD000, 0x0907)
	// Every pixel in the 16×8 cell should now be lit (BG=non-zero fills).
	for y := 50; y < 58; y++ {
		for x := 100; x < 116; x++ {
			if !isLit(c, x, y) {
				t.Errorf("BG-fill pixel (%d,%d) not lit", x, y)
			}
		}
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
